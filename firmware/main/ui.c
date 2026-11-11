// Beltpack UI: key logic (tap = latch, hold = momentary), encoder (turn =
// volume of selected key, press = listen, double press = call, hold = menu),
// display rendering and key LEDs.
#include "ui.h"

#include <stdio.h>
#include <string.h>

#include "esp_system.h"
#include "esp_timer.h"
#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"
#include "freertos/task.h"

#include "audio.h"
#include "ble_link.h"
#include "codec_wm8960.h"
#include "display.h"
#include "keys.h"
#include "settings.h"

ui_state_t g_ui;
static SemaphoreHandle_t s_mu;
static const ui_transport_t *s_tr;
static volatile bool s_dirty = true;

#define LATCH_MS 350
#define LONG_MS 800
#define DOUBLE_MS 300

static int64_t s_key_down_us[4];
static bool s_key_ignore_up[4];
static int64_t s_enc_down_us, s_enc_last_up_us;
static bool s_enc_down, s_enc_long_fired, s_enc_pending_single;
static int s_call_key = -1;
static int64_t s_call_until_us;

// ---- menu ----
typedef enum { M_NONE = 0, M_LIST, M_EDIT } menu_state_t;
static menu_state_t s_menu;
static int s_menu_idx;
enum { MI_MICTYPE, MI_MICGAIN, MI_SIDETONE, MI_HP, MI_BRIGHT, MI_INFO, MI_BTSETUP, MI_MODE, MI_REBOOT, MI_EXIT, MI_COUNT };
static const char *MENU_NAMES[MI_COUNT] = {"MIC TYPE", "MIC GAIN", "SIDETONE", "HEADPHONE", "BRIGHTNESS", "INFO", "BT SETUP", "MODE", "REBOOT", "EXIT"};

static int64_t now_us(void) { return esp_timer_get_time(); }
void ui_lock(void) { xSemaphoreTakeRecursive(s_mu, portMAX_DELAY); }
void ui_unlock(void) { xSemaphoreGiveRecursive(s_mu); }
void ui_changed(void) { s_dirty = true; }
void ui_set_transport(const ui_transport_t *t) { s_tr = t; }

void ui_overlay(const char *text, int ms)
{
    ui_lock();
    strncpy(g_ui.overlay, text, sizeof(g_ui.overlay) - 1);
    g_ui.overlay_until_us = now_us() + ms * 1000LL;
    ui_unlock();
    ui_changed();
}

bool ui_any_talk(void)
{
    for (int i = 0; i < 4; i++)
        if (g_ui.keys[i].talk) return true;
    return false;
}

float ui_gain_for_mask(uint32_t mask)
{
    float g = 0;
    for (int i = 0; i < 4; i++) {
        if (!(mask & (1u << i)) || !g_ui.keys[i].present) continue;
        float v = g_ui.keys[i].vol / 75.0f;
        if (v * v > g) g = v * v;
    }
    return g;
}

// ---------------- keys ----------------
static void set_talk(int k, bool on)
{
    ui_key_t *key = &g_ui.keys[k];
    if (!key->present || (on && (!key->can_talk || g_ui.muted))) {
        if (on && g_ui.muted) ui_overlay("MIC MUTED", 1200);
        return;
    }
    key->talk = on; // optimistic; server/app echo confirms
    if (s_tr && s_tr->talk) s_tr->talk(k, on);
    audio_set_transmit(ui_any_talk());
    ui_changed();
}

void ui_key_event(int k, bool down)
{
    if (k < 0 || k > 3) return;
    ui_lock();
    if (s_menu != M_NONE) { // keys exit the menu
        if (down) s_menu = M_NONE;
        ui_unlock();
        ui_changed();
        return;
    }
    g_ui.selected = k;
    if (down) {
        s_key_down_us[k] = now_us();
        if (g_ui.keys[k].talk) { // latched -> unlatch on press
            set_talk(k, false);
            s_key_ignore_up[k] = true;
        } else {
            s_key_ignore_up[k] = false;
            set_talk(k, true);
        }
    } else if (!s_key_ignore_up[k]) {
        if (now_us() - s_key_down_us[k] >= LATCH_MS * 1000LL) set_talk(k, false); // momentary
    }
    ui_unlock();
    ui_changed();
}

// ---------------- encoder ----------------
static void apply_audio_settings(void)
{
    codec_set_mic(g_cfg.mic_electret, g_cfg.mic_gain);
    codec_set_sidetone(g_cfg.sidetone);
    codec_set_hp(g_cfg.hp_level);
    disp_contrast(g_cfg.bright);
    keys_led_brightness(g_cfg.bright);
}

static uint8_t *menu_value(int idx)
{
    switch (idx) {
    case MI_MICGAIN: return &g_cfg.mic_gain;
    case MI_SIDETONE: return &g_cfg.sidetone;
    case MI_HP: return &g_cfg.hp_level;
    case MI_BRIGHT: return &g_cfg.bright;
    }
    return NULL;
}

void ui_enc_turn(int d)
{
    ui_lock();
    if (s_menu == M_LIST) {
        s_menu_idx = (s_menu_idx + d + MI_COUNT) % MI_COUNT;
    } else if (s_menu == M_EDIT) {
        uint8_t *v = menu_value(s_menu_idx);
        if (v) {
            int n = *v + d * 5;
            *v = n < 0 ? 0 : n > 100 ? 100 : n;
            apply_audio_settings();
        } else if (s_menu_idx == MI_MICTYPE) {
            g_cfg.mic_electret = !g_cfg.mic_electret;
            apply_audio_settings();
        } else if (s_menu_idx == MI_MODE) {
            g_cfg.mode = (g_cfg.mode + (d > 0 ? 1 : 2)) % 3;
        }
    } else {
        ui_key_t *k = &g_ui.keys[g_ui.selected];
        if (k->present) {
            int n = k->vol + d * 5;
            k->vol = n < 0 ? 0 : n > 100 ? 100 : n;
            if (s_tr && s_tr->vol) s_tr->vol(g_ui.selected, k->vol);
            char buf[24];
            snprintf(buf, sizeof(buf), "%.6s %d", k->name, k->vol);
            ui_overlay(buf, 1200);
        }
    }
    ui_unlock();
    ui_changed();
}

static void menu_select(void)
{
    switch (s_menu_idx) {
    case MI_INFO: {
        char buf[24];
        snprintf(buf, sizeof(buf), "%s", g_ui.ip[0] ? g_ui.ip : settings_device_id());
        ui_overlay(buf, 3000);
        break;
    }
    case MI_BTSETUP:
        ble_link_start_provisioning(300);
        ui_overlay("BT SETUP ON", 1500);
        s_menu = M_NONE;
        break;
    case MI_REBOOT:
        settings_save();
        esp_restart();
        break;
    case MI_EXIT:
        s_menu = M_NONE;
        break;
    default:
        s_menu = M_EDIT;
    }
}

void ui_enc_button(bool down)
{
    ui_lock();
    int64_t t = now_us();
    if (down) {
        s_enc_down = true;
        s_enc_long_fired = false;
        s_enc_down_us = t;
    } else if (s_enc_down) {
        s_enc_down = false;
        if (!s_enc_long_fired) {
            if (s_menu == M_LIST) menu_select();
            else if (s_menu == M_EDIT) {
                s_menu = M_LIST;
                settings_save();
                if (s_menu_idx == MI_MODE) ui_overlay("REBOOT TO APPLY", 1500);
            } else if (s_enc_pending_single && t - s_enc_last_up_us < DOUBLE_MS * 1000LL) {
                // double press: call the selected channel for 2 s
                s_enc_pending_single = false;
                int k = g_ui.selected;
                if (g_ui.keys[k].present && s_tr && s_tr->call) {
                    s_tr->call(k, true);
                    s_call_key = k;
                    s_call_until_us = t + 2000000;
                    ui_overlay("CALLING", 1500);
                }
            } else {
                s_enc_pending_single = true;
            }
            s_enc_last_up_us = t;
        }
    }
    ui_unlock();
    ui_changed();
}

// called every 10 ms
static void ui_tick(void)
{
    int64_t t = now_us();
    ui_lock();
    if (s_enc_down && !s_enc_long_fired && t - s_enc_down_us > LONG_MS * 1000LL) {
        s_enc_long_fired = true;
        if (s_menu == M_NONE) { s_menu = M_LIST; s_menu_idx = 0; }
        else { s_menu = M_NONE; settings_save(); }
        s_dirty = true;
    }
    if (s_enc_pending_single && t - s_enc_last_up_us > DOUBLE_MS * 1000LL) {
        s_enc_pending_single = false; // single press: toggle listen on selected key
        ui_key_t *k = &g_ui.keys[g_ui.selected];
        if (k->present && (k->can_listen || k->listen)) {
            k->listen = !k->listen;
            if (s_tr && s_tr->listen) s_tr->listen(g_ui.selected, k->listen);
            ui_overlay(k->listen ? "LISTEN ON" : "LISTEN OFF", 800);
        }
    }
    if (s_call_key >= 0 && t > s_call_until_us) {
        if (s_tr && s_tr->call) s_tr->call(s_call_key, false);
        s_call_key = -1;
    }
    // activity from the audio engine (network mode); BLE mode sets `active` from the app
    if (g_cfg.mode != MODE_BLE) {
        uint32_t m = audio_active_mask();
        for (int i = 0; i < 4; i++) {
            bool a = (m >> i) & 1;
            if (a != g_ui.keys[i].active) { g_ui.keys[i].active = a; s_dirty = true; }
        }
    }
    ui_unlock();
}

// ---------------- rendering ----------------
static void render(void)
{
    int W = disp_w(), H = disp_h();
    bool eink = disp_is_eink();
    bool blink = (now_us() / 400000) & 1;
    disp_clear();

    // status bar (inverted)
    int sb = 16;
    disp_fill(0, 0, W, sb, true);
    const char *who = g_ui.conn == CONN_ONLINE ? g_ui.user : (g_ui.status[0] ? g_ui.status : "CONNECTING");
    char name[17];
    snprintf(name, sizeof(name), "%.*s", (W - 40) / 8, who);
    for (char *p = name; *p; p++) if (*p >= 'a' && *p <= 'z') *p -= 32;
    disp_text(1, 0, name, true, true);
    const char *mode = g_cfg.mode == MODE_WIFI ? "WIFI" : g_cfg.mode == MODE_ETH ? "ETH" : "BT";
    char m2[8];
    snprintf(m2, sizeof(m2), "%s", mode);
    // right side: mode on line 1, battery on line 2 (small font, drawn inverted on bar)
    disp_text(W - (int)strlen(m2) * 6 - 1, 0, m2, false, true);
    {
        char b[8];
        if (g_ui.battery < 0) snprintf(b, sizeof(b), "EXT");
        else snprintf(b, sizeof(b), "%d%%", g_ui.battery);
        disp_text(W - (int)strlen(b) * 6 - 1, 8, b, false, true);
    }

    if (s_menu != M_NONE) {
        int rows = (H - sb) / 16;
        int first = s_menu_idx - rows / 2;
        if (first < 0) first = 0;
        if (first > MI_COUNT - rows) first = MI_COUNT - rows;
        for (int r = 0; r < rows && first + r < MI_COUNT; r++) {
            int idx = first + r;
            char line[24];
            uint8_t *v = menu_value(idx);
            if (v) snprintf(line, sizeof(line), "%-10s%3d", MENU_NAMES[idx], *v);
            else if (idx == MI_MICTYPE) snprintf(line, sizeof(line), "MIC %s", g_cfg.mic_electret ? "ELECTRET" : "DYNAMIC");
            else if (idx == MI_MODE) snprintf(line, sizeof(line), "MODE %s", g_cfg.mode == MODE_WIFI ? "WIFI" : g_cfg.mode == MODE_ETH ? "ETH" : "BT");
            else snprintf(line, sizeof(line), "%s", MENU_NAMES[idx]);
            bool sel = idx == s_menu_idx;
            int y = sb + r * 16;
            if (sel) disp_fill(0, y, W, 16, true);
            disp_text(2, y, line, true, sel);
            if (sel && s_menu == M_EDIT && blink) disp_text(W - 9, y, "<", true, true);
        }
        disp_flush();
        return;
    }

    // 2 x 2 key cells
    int cw = W / 2, ch = (H - sb) / 2;
    for (int i = 0; i < 4; i++) {
        ui_key_t *k = &g_ui.keys[i];
        int x = (i % 2) * cw, y = sb + (i / 2) * ch;
        int pad = 2;
        if (!k->present) continue;
        bool inv = k->talk || (k->call_in && blink);
        if (inv) disp_fill(x + 1, y + 1, cw - 2, ch - 2, true);
        else if (k->listen) disp_rect(x + 1, y + 1, cw - 2, ch - 2, true);
        else { // not listening: dashed outline
            for (int dx = 1; dx < cw - 1; dx += 4) { disp_pixel(x + dx, y + 1, true); disp_pixel(x + dx, y + ch - 2, true); }
        }
        if (i == g_ui.selected) disp_fill(x + 1, y + ch - 3, cw - 2, 2, !inv);
        char nm[16];
        int maxc = (cw - 2 * pad - 10) / 8;
        snprintf(nm, sizeof(nm), "%.*s", maxc, k->name);
        for (char *p = nm; *p; p++) if (*p >= 'a' && *p <= 'z') *p -= 32;
        int ty = y + (ch - 16) / 2;
        disp_text(x + pad + 2, ty, nm, true, inv);
        // state glyph at right edge of cell
        const char *g = k->talk ? "T" : k->call_in ? "!" : (!eink && k->active) ? ")" : !k->listen ? "-" : "";
        disp_text(x + cw - 10 - pad, ty, g, true, inv);
    }

    // overlays
    int64_t t = now_us();
    const char *ov = NULL;
    if (t < g_ui.overlay_until_us) ov = g_ui.overlay;
    else if (g_ui.muted && blink) ov = "MIC MUTED";
    else if (g_ui.bt_setup) ov = "BT SETUP";
    if (ov) {
        int w = (int)strlen(ov) * 8 + 8;
        if (w > W) w = W;
        int x = (W - w) / 2, y = sb + (H - sb - 20) / 2;
        disp_fill(x, y, w, 20, false);
        disp_rect(x, y, w, 20, true);
        disp_text(x + 4, y + 2, ov, true, false);
    }
    if (t < g_ui.identify_until_us && blink) disp_invert_all();
    disp_flush();
}

static uint32_t scale(uint32_t c, int pct)
{
    uint32_t r = ((c >> 16) & 0xFF) * pct / 100, g = ((c >> 8) & 0xFF) * pct / 100, b = (c & 0xFF) * pct / 100;
    return (r << 16) | (g << 8) | b;
}

static void update_leds(void)
{
    bool blink = (now_us() / 250000) & 1;
    bool ident = now_us() < g_ui.identify_until_us;
    for (int i = 0; i < 4; i++) {
        ui_key_t *k = &g_ui.keys[i];
        uint32_t c = 0;
        if (ident) c = blink ? 0xFFFFFF : 0;
        else if (!k->present) c = 0;
        else if (k->talk) c = k->color;                       // talking: full colour
        else if (k->call_in) c = blink ? 0xFFFFFF : k->color; // call: flash white
        else if (k->active && k->listen) c = scale(k->color, 45);
        else if (k->listen) c = scale(k->color, 12);
        else c = 0;
        keys_set_led(i, c);
    }
    keys_led_refresh();
}

static void ui_task(void *arg)
{
    int64_t last_render = 0;
    for (;;) {
        ui_tick();
        int64_t t = now_us();
        bool eink = disp_is_eink();
        // OLED: up to 20 fps when something changed (and 2.5 Hz for blinking)
        // e-ink: at most once per second, and only when content changed
        int64_t period = eink ? 1000000 : 50000;
        bool anim = !eink && (t / 400000 != last_render / 400000);
        if ((s_dirty || anim) && t - last_render >= period) {
            s_dirty = false;
            last_render = t;
            ui_lock();
            render();
            ui_unlock();
        }
        ui_lock();
        update_leds();
        ui_unlock();
        vTaskDelay(pdMS_TO_TICKS(10));
    }
}

void ui_init(void)
{
    s_mu = xSemaphoreCreateRecursiveMutex();
    memset(&g_ui, 0, sizeof(g_ui));
    for (int i = 0; i < 4; i++) g_ui.keys[i].vol = 75;
    g_ui.battery = -1;
    apply_audio_settings();
    xTaskCreatePinnedToCore(ui_task, "ui", 6144, NULL, 5, NULL, 0);
}
