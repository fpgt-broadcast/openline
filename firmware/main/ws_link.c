// Standalone network mode: talks the OpenLine protocol (docs/PROTOCOL.md)
// directly to the server over a WebSocket (wss:// with the CA bundle).
#include "ws_link.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "cJSON.h"
#include "esp_crt_bundle.h"
#include "esp_heap_caps.h"
#include "esp_log.h"
#include "esp_system.h"
#include "esp_timer.h"
#include "esp_websocket_client.h"
#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"
#include "freertos/task.h"

#include "audio.h"
#include "codec_wm8960.h"
#include "display.h"
#include "keys.h"
#include "net.h"
#include "power.h"
#include "settings.h"
#include "ui.h"

static const char *TAG = "ws";
#define FW_VERSION "0.1.0"
#define RXBUF 16384

static esp_websocket_client_handle_t s_ws;
static QueueHandle_t s_txq; // char* JSON strings (heap) to send
static char *s_rx;          // text reassembly buffer
static int s_ch_ids[32], s_nch;
static uint16_t s_my_sid;
static volatile bool s_connected;

// ---------------- outgoing ----------------
static void send_json(cJSON *o)
{
    char *s = cJSON_PrintUnformatted(o);
    cJSON_Delete(o);
    if (s && xQueueSend(s_txq, &s, 0) != pdTRUE) free(s);
}

static void send_state(const char *t, int key, bool on)
{
    if (key < 0 || key >= 4 || !g_ui.keys[key].present) return;
    cJSON *o = cJSON_CreateObject();
    cJSON_AddStringToObject(o, "t", t);
    cJSON_AddNumberToObject(o, "ch", g_ui.keys[key].ch_id);
    cJSON_AddBoolToObject(o, "on", on);
    send_json(o);
}

static void tr_talk(int k, bool on) { send_state("talk", k, on); }
static void tr_listen(int k, bool on) { send_state("listen", k, on); }
static void tr_call(int k, bool on) { send_state("call", k, on); }
static const ui_transport_t TRANSPORT = {.talk = tr_talk, .listen = tr_listen, .call = tr_call, .vol = NULL};

static void on_mic(const uint8_t *opus, int len, uint16_t seq)
{
    if (!s_connected || g_ui.muted) return;
    uint8_t buf[3 + 400];
    if (len > 400) return;
    buf[0] = 0x01;
    buf[1] = seq >> 8;
    buf[2] = seq & 0xFF;
    memcpy(buf + 3, opus, len);
    esp_websocket_client_send_bin(s_ws, (const char *)buf, len + 3, pdMS_TO_TICKS(10));
}

static void tx_task(void *arg)
{
    char *s;
    int64_t last_tel = 0;
    for (;;) {
        if (xQueueReceive(s_txq, &s, pdMS_TO_TICKS(500)) == pdTRUE) {
            if (s_connected || strstr(s, "\"hello\"")) esp_websocket_client_send_text(s_ws, s, strlen(s), pdMS_TO_TICKS(1000));
            free(s);
        }
        int64_t now = esp_timer_get_time();
        if (s_connected && now - last_tel > 5000000) {
            last_tel = now;
            cJSON *o = cJSON_CreateObject();
            ui_lock();
            cJSON_AddStringToObject(o, "t", "telemetry");
            cJSON_AddNumberToObject(o, "bat", g_ui.battery);
            cJSON_AddBoolToObject(o, "chg", g_ui.charging);
            cJSON_AddNumberToObject(o, "rssi", net_rssi());
            cJSON_AddStringToObject(o, "net", g_cfg.mode == MODE_ETH ? "ethernet" : "wifi");
            cJSON_AddStringToObject(o, "ip", g_ui.ip);
            cJSON_AddStringToObject(o, "fw", FW_VERSION);
            cJSON_AddNumberToObject(o, "up", (double)(now / 1000000));
            cJSON_AddBoolToObject(o, "hs", g_ui.headset);
            cJSON_AddBoolToObject(o, "poe", g_cfg.mode == MODE_ETH && power_external());
            cJSON_AddStringToObject(o, "disp", disp_is_eink() ? "eink" : "oled");
            ui_unlock();
            send_json(o);
        }
    }
}

// ---------------- incoming ----------------
static int key_of_channel(int id)
{
    for (int i = 0; i < 4; i++)
        if (g_ui.keys[i].present && g_ui.keys[i].ch_id == id) return i;
    return -1;
}

static bool in_array(cJSON *arr, int id)
{
    cJSON *it;
    cJSON_ArrayForEach(it, arr) if (cJSON_IsNumber(it) && it->valueint == id) return true;
    return false;
}

static uint32_t parse_color(const char *s)
{
    if (!s || s[0] != '#') return 0x808080;
    return (uint32_t)strtoul(s + 1, NULL, 16);
}

static void apply_self(cJSON *talk, cJSON *listen, bool muted)
{
    for (int i = 0; i < 4; i++) {
        ui_key_t *k = &g_ui.keys[i];
        if (!k->present) continue;
        k->talk = in_array(talk, k->ch_id);
        k->listen = in_array(listen, k->ch_id);
    }
    g_ui.muted = muted;
    audio_set_transmit(ui_any_talk() && !muted);
}

static void handle_text(const char *txt)
{
    cJSON *m = cJSON_Parse(txt);
    if (!m) return;
    const char *t = cJSON_GetStringValue(cJSON_GetObjectItem(m, "t"));
    if (!t) goto done;
    ui_lock();
    if (!strcmp(t, "welcome")) {
        s_my_sid = (uint16_t)cJSON_GetNumberValue(cJSON_GetObjectItem(m, "sid"));
        cJSON *chs = cJSON_GetObjectItem(m, "channels");
        cJSON *listen = cJSON_GetObjectItem(m, "listen");
        s_nch = 0;
        cJSON *c;
        cJSON_ArrayForEach(c, chs)
        {
            int id = (int)cJSON_GetNumberValue(cJSON_GetObjectItem(c, "id"));
            if (s_nch < 32) s_ch_ids[s_nch] = id;
            if (s_nch < 4) {
                ui_key_t *k = &g_ui.keys[s_nch];
                uint8_t vol = k->ch_id == id && k->present ? k->vol : 75;
                memset(k, 0, sizeof(*k));
                k->present = true;
                k->ch_id = id;
                k->vol = vol;
                snprintf(k->name, sizeof(k->name), "%s", cJSON_GetStringValue(cJSON_GetObjectItem(c, "name")) ?: "?");
                k->color = parse_color(cJSON_GetStringValue(cJSON_GetObjectItem(c, "color")));
                k->can_talk = cJSON_IsTrue(cJSON_GetObjectItem(c, "canTalk"));
                k->can_listen = cJSON_IsTrue(cJSON_GetObjectItem(c, "canListen"));
            } else if (in_array(listen, id)) {
                // only 4 keys: stop listening to channels that have no key
                cJSON *o = cJSON_CreateObject();
                cJSON_AddStringToObject(o, "t", "listen");
                cJSON_AddNumberToObject(o, "ch", id);
                cJSON_AddBoolToObject(o, "on", false);
                send_json(o);
            }
            s_nch++;
        }
        for (int i = s_nch; i < 4; i++) memset(&g_ui.keys[i], 0, sizeof(ui_key_t));
        apply_self(cJSON_GetObjectItem(m, "talk"), listen, cJSON_IsTrue(cJSON_GetObjectItem(m, "muted")));
        snprintf(g_ui.user, sizeof(g_ui.user), "%s", cJSON_GetStringValue(cJSON_GetObjectItem(m, "user")) ?: "");
        if (s_nch == 0) snprintf(g_ui.user, sizeof(g_ui.user), "ID %s", settings_device_id() + 6);
        g_ui.conn = CONN_ONLINE;
        g_ui.status[0] = 0;
    } else if (!strcmp(t, "self")) {
        apply_self(cJSON_GetObjectItem(m, "talk"), cJSON_GetObjectItem(m, "listen"), cJSON_IsTrue(cJSON_GetObjectItem(m, "muted")));
    } else if (!strcmp(t, "state")) {
        cJSON *c;
        cJSON_ArrayForEach(c, cJSON_GetObjectItem(m, "channels"))
        {
            int k = key_of_channel((int)cJSON_GetNumberValue(cJSON_GetObjectItem(c, "id")));
            if (k < 0) continue;
            bool call = false;
            cJSON *p;
            cJSON_ArrayForEach(p, cJSON_GetObjectItem(c, "calls"))
            {
                if ((uint16_t)cJSON_GetNumberValue(cJSON_GetObjectItem(p, "sid")) != s_my_sid) call = true;
            }
            g_ui.keys[k].call_in = call;
        }
    } else if (!strcmp(t, "remote")) {
        const char *op = cJSON_GetStringValue(cJSON_GetObjectItem(m, "op"));
        if (op && !strcmp(op, "vol")) {
            int k = key_of_channel((int)cJSON_GetNumberValue(cJSON_GetObjectItem(m, "ch")));
            if (k >= 0) g_ui.keys[k].vol = (uint8_t)cJSON_GetNumberValue(cJSON_GetObjectItem(m, "v"));
        } else if (op && !strcmp(op, "identify")) {
            g_ui.identify_until_us = esp_timer_get_time() + 4000000;
        } else if (op && !strcmp(op, "reboot")) {
            ui_unlock();
            cJSON_Delete(m);
            vTaskDelay(pdMS_TO_TICKS(300));
            esp_restart();
        }
    } else if (!strcmp(t, "assign")) {
        const char *tok = cJSON_GetStringValue(cJSON_GetObjectItem(m, "token"));
        snprintf(g_cfg.token, sizeof(g_cfg.token), "%s", tok ? tok : "");
        settings_save();
        ESP_LOGI(TAG, "re-assigned; reconnecting");
        ui_unlock();
        cJSON_Delete(m);
        esp_restart(); // simplest way to come back with the new identity
    } else if (!strcmp(t, "netcfg")) {
        static const char *keys[] = {"mode", "ip", "mask", "gw", "dns", "ssid", "pass", "server"};
        for (size_t i = 0; i < sizeof(keys) / sizeof(*keys); i++) {
            const char *v = cJSON_GetStringValue(cJSON_GetObjectItem(m, keys[i]));
            if (v && (v[0] || !strcmp(keys[i], "ip"))) settings_set_kv(&g_cfg, keys[i], v);
        }
        g_cfg.dhcp = cJSON_IsTrue(cJSON_GetObjectItem(m, "dhcp"));
        settings_save();
        ui_unlock();
        cJSON_Delete(m);
        ui_overlay("NETWORK SET", 2000);
        vTaskDelay(pdMS_TO_TICKS(1500));
        esp_restart();
    } else if (!strcmp(t, "settings")) {
        static const char *keys[] = {"micType", "name"};
        for (size_t i = 0; i < 2; i++) {
            const char *v = cJSON_GetStringValue(cJSON_GetObjectItem(m, keys[i]));
            if (v) settings_set_kv(&g_cfg, keys[i], v);
        }
        static const char *nums[] = {"micGain", "sidetone", "hpLevel", "bright"};
        for (size_t i = 0; i < 4; i++) {
            cJSON *v = cJSON_GetObjectItem(m, nums[i]);
            if (cJSON_IsNumber(v)) {
                char b[8];
                snprintf(b, sizeof(b), "%d", v->valueint);
                settings_set_kv(&g_cfg, nums[i], b);
            }
        }
        settings_save();
        codec_set_mic(g_cfg.mic_electret, g_cfg.mic_gain);
        codec_set_sidetone(g_cfg.sidetone);
        codec_set_hp(g_cfg.hp_level);
        disp_contrast(g_cfg.bright);
        keys_led_brightness(g_cfg.bright);
    } else if (!strcmp(t, "error")) {
        const char *msg = cJSON_GetStringValue(cJSON_GetObjectItem(m, "msg"));
        ESP_LOGW(TAG, "server: %s", msg ? msg : "?");
        if (msg && strstr(msg, "login failed")) snprintf(g_ui.status, sizeof(g_ui.status), "LOGIN FAILED");
    }
    ui_unlock();
    ui_changed();
done:
    cJSON_Delete(m);
}

static void on_ws(void *arg, esp_event_base_t base, int32_t id, void *data)
{
    esp_websocket_event_data_t *d = data;
    switch (id) {
    case WEBSOCKET_EVENT_CONNECTED: {
        ESP_LOGI(TAG, "connected");
        cJSON *o = cJSON_CreateObject();
        cJSON_AddStringToObject(o, "t", "hello");
        cJSON_AddNumberToObject(o, "ver", 1);
        cJSON_AddStringToObject(o, "client", "beltpack");
        cJSON_AddStringToObject(o, "device", settings_device_id());
        if (g_cfg.token[0]) cJSON_AddStringToObject(o, "token", g_cfg.token);
        send_json(o);
        s_connected = true;
        break;
    }
    case WEBSOCKET_EVENT_DISCONNECTED:
    case WEBSOCKET_EVENT_ERROR:
        if (s_connected) ESP_LOGW(TAG, "disconnected");
        s_connected = false;
        audio_set_transmit(false);
        ui_lock();
        g_ui.conn = CONN_CONNECTING;
        if (!g_ui.status[0] || !strcmp(g_ui.status, "CONNECTING")) snprintf(g_ui.status, sizeof(g_ui.status), "NO SERVER");
        ui_unlock();
        ui_changed();
        break;
    case WEBSOCKET_EVENT_DATA:
        if (d->op_code == 0x2 && d->payload_offset == 0 && d->data_len == d->payload_len) {
            const uint8_t *b = (const uint8_t *)d->data_ptr;
            if (d->data_len > 9 && b[0] == 0x02) {
                uint16_t sid = (b[1] << 8) | b[2];
                uint32_t mask = ((uint32_t)b[5] << 24) | (b[6] << 16) | (b[7] << 8) | b[8];
                audio_rx(sid, mask, b + 9, d->data_len - 9);
            }
        } else if (d->op_code == 0x1 || (d->op_code == 0x0 && d->payload_offset > 0)) {
            if (d->payload_len >= RXBUF || d->payload_offset + d->data_len > RXBUF - 1) break;
            memcpy(s_rx + d->payload_offset, d->data_ptr, d->data_len);
            if (d->payload_offset + d->data_len == d->payload_len) {
                s_rx[d->payload_len] = 0;
                handle_text(s_rx);
            }
        }
        break;
    }
}

void ws_link_start(void)
{
    static bool started;
    if (started) return; // client auto-reconnects on network changes
    started = true;
    s_rx = heap_caps_malloc(RXBUF, MALLOC_CAP_SPIRAM);
    s_txq = xQueueCreate(16, sizeof(char *));
    ui_set_transport(&TRANSPORT);
    audio_init(on_mic, ui_gain_for_mask);
    esp_websocket_client_config_t cfg = {
        .uri = g_cfg.server,
        .buffer_size = 2048,
        .task_stack = 8192,
        .task_prio = 15,
        .crt_bundle_attach = esp_crt_bundle_attach,
        .reconnect_timeout_ms = 2000,
        .network_timeout_ms = 8000,
        .ping_interval_sec = 10,
    };
    s_ws = esp_websocket_client_init(&cfg);
    esp_websocket_register_events(s_ws, WEBSOCKET_EVENT_ANY, on_ws, NULL);
    esp_websocket_client_start(s_ws);
    xTaskCreate(tx_task, "ws_tx", 4096, NULL, 8, NULL);
    ESP_LOGI(TAG, "connecting to %s as device %s", g_cfg.server, settings_device_id());
}
