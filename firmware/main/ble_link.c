// Bluetooth LE link to the OpenLine app (web/static/ble.js):
//  - bridge mode: keys/screen <-> app over JSON, and (optionally) mic/headset
//    audio as Opus over GATT. In "phone audio" mode only keys/screen are used
//    and audio goes through the phone (AirPods etc.).
//  - provisioning: the admin page writes Wi-Fi / server / token settings.
#include "ble_link.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "cJSON.h"
#include "esp_log.h"
#include "esp_system.h"
#include "esp_timer.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "host/ble_hs.h"
#include "host/util/util.h"
#include "nimble/nimble_port.h"
#include "nimble/nimble_port_freertos.h"
#include "services/gap/ble_svc_gap.h"
#include "services/gatt/ble_svc_gatt.h"

#include "audio.h"
#include "settings.h"
#include "ui.h"

static const char *TAG = "ble";

// 4f4c00NN-8e2b-4d6e-9c1a-5b3f2a7d1e60 (little-endian byte order)
#define OL_UUID(n) BLE_UUID128_INIT(0x60, 0x1e, 0x7d, 0x2a, 0x3f, 0x5b, 0x1a, 0x9c, 0x6e, 0x4d, 0x2b, 0x8e, (n), 0x00, 0x4c, 0x4f)
static const ble_uuid128_t U_SVC = OL_UUID(0x01), U_CTRL_RX = OL_UUID(0x02), U_CTRL_TX = OL_UUID(0x03),
                           U_AUDIO_UP = OL_UUID(0x04), U_AUDIO_DN = OL_UUID(0x05), U_PROV = OL_UUID(0x06),
                           U_INFO = OL_UUID(0x07);
enum { C_CTRL_RX = 1, C_CTRL_TX, C_AUDIO_UP, C_AUDIO_DN, C_PROV, C_INFO };

static uint16_t h_ctrl_tx, h_audio_up;
static uint16_t s_conn = BLE_HS_CONN_HANDLE_NONE;
static bool s_sub_ctrl, s_sub_audio;
static uint8_t s_own_addr;
static bool s_bridge_mode;    // device runs in MODE_BLE
static volatile bool s_audio_via_belt = true;
static int64_t s_prov_until_us; // provisioning window in network modes
static ol_settings_t s_pending;
static bool s_pending_init;
static char s_name[20];

static void advertise(void);

// ---------------- outgoing ----------------
static void notify(uint16_t h, const void *data, size_t len)
{
    if (s_conn == BLE_HS_CONN_HANDLE_NONE) return;
    struct os_mbuf *om = ble_hs_mbuf_from_flat(data, len);
    if (om) ble_gatts_notify_custom(s_conn, h, om);
}

static void ctrl_event(const char *t, int k, const char *field, int v)
{
    if (!s_sub_ctrl) return;
    char buf[64];
    int n = snprintf(buf, sizeof(buf), "{\"t\":\"%s\",\"k\":%d,\"%s\":%d}", t, k, field, v);
    notify(h_ctrl_tx, buf, n);
}

static void tr_talk(int k, bool on) { ctrl_event("talk", k, "on", on); }
static void tr_listen(int k, bool on) { ctrl_event("listen", k, "on", on); }
static void tr_call(int k, bool on) { ctrl_event("call", k, "on", on); }
static void tr_vol(int k, int v) { ctrl_event("vol", k, "v", v); }
static const ui_transport_t TRANSPORT = {.talk = tr_talk, .listen = tr_listen, .call = tr_call, .vol = tr_vol};

static void on_mic(const uint8_t *opus, int len, uint16_t seq)
{
    if (!s_sub_audio || !s_audio_via_belt || len > 240) return;
    uint8_t buf[2 + 240];
    buf[0] = seq >> 8;
    buf[1] = seq & 0xFF;
    memcpy(buf + 2, opus, len);
    notify(h_audio_up, buf, len + 2);
}

static float gain_unity(uint32_t mask) { (void)mask; return 1.0f; } // the app already mixed

// ---------------- incoming ----------------
static int ival(cJSON *o, const char *k, int def)
{
    cJSON *v = cJSON_GetObjectItem(o, k);
    return cJSON_IsNumber(v) ? v->valueint : cJSON_IsBool(v) ? cJSON_IsTrue(v) : def;
}

static void handle_ctrl(const char *json)
{
    cJSON *m = cJSON_Parse(json);
    if (!m) return;
    const char *t = cJSON_GetStringValue(cJSON_GetObjectItem(m, "t"));
    ui_lock();
    if (t && !strcmp(t, "key")) {
        int k = ival(m, "k", -1);
        if (k >= 0 && k < 4) {
            ui_key_t *key = &g_ui.keys[k];
            const char *n = cJSON_GetStringValue(cJSON_GetObjectItem(m, "n"));
            key->present = n && n[0];
            if (key->present) {
                snprintf(key->name, sizeof(key->name), "%s", n);
                const char *c = cJSON_GetStringValue(cJSON_GetObjectItem(m, "c"));
                key->color = c ? (uint32_t)strtoul(c, NULL, 16) : 0x808080;
                key->can_talk = ival(m, "ct", 0);
                key->can_listen = ival(m, "cl", 1);
                key->talk = ival(m, "tk", 0);
                key->listen = ival(m, "ls", 0);
                key->active = ival(m, "ac", 0);
                key->call_in = ival(m, "ca", 0);
                key->vol = (uint8_t)ival(m, "v", 75);
            }
            audio_set_transmit(ui_any_talk());
        }
    } else if (t && !strcmp(t, "st")) {
        snprintf(g_ui.user, sizeof(g_ui.user), "%s", cJSON_GetStringValue(cJSON_GetObjectItem(m, "u")) ?: "");
        g_ui.conn = ival(m, "on", 0) ? CONN_ONLINE : CONN_CONNECTING;
        snprintf(g_ui.status, sizeof(g_ui.status), "%s", g_ui.conn == CONN_ONLINE ? "" : "APP OFFLINE");
        g_ui.muted = ival(m, "m", 0);
    } else if (t && !strcmp(t, "mode")) {
        s_audio_via_belt = ival(m, "audio", 1);
    } else if (t && !strcmp(t, "identify")) {
        g_ui.identify_until_us = esp_timer_get_time() + 4000000;
    }
    ui_unlock();
    ui_changed();
    cJSON_Delete(m);
}

static void handle_prov(const char *json)
{
    cJSON *m = cJSON_Parse(json);
    if (!m) return;
    if (!s_pending_init) { s_pending = g_cfg; s_pending_init = true; }
    if (cJSON_IsTrue(cJSON_GetObjectItem(m, "commit")) || ival(m, "commit", 0)) {
        g_cfg = s_pending;
        settings_save();
        ESP_LOGI(TAG, "provisioned: mode=%d server=%s; rebooting", g_cfg.mode, g_cfg.server);
        cJSON_Delete(m);
        ui_overlay("SAVED", 1000);
        vTaskDelay(pdMS_TO_TICKS(800));
        esp_restart();
    }
    const char *k = cJSON_GetStringValue(cJSON_GetObjectItem(m, "k"));
    cJSON *v = cJSON_GetObjectItem(m, "v");
    char buf[160];
    if (k && v) {
        if (cJSON_IsString(v)) snprintf(buf, sizeof(buf), "%s", v->valuestring);
        else if (cJSON_IsNumber(v)) snprintf(buf, sizeof(buf), "%d", v->valueint);
        else snprintf(buf, sizeof(buf), "%d", cJSON_IsTrue(v));
        if (!settings_set_kv(&s_pending, k, buf)) ESP_LOGW(TAG, "unknown key %s", k);
    }
    cJSON_Delete(m);
}

static int chr_access(uint16_t conn, uint16_t attr, struct ble_gatt_access_ctxt *ctxt, void *arg)
{
    int which = (int)(intptr_t)arg;
    if (ctxt->op == BLE_GATT_ACCESS_OP_READ_CHR && which == C_INFO) {
        char buf[160];
        int n = snprintf(buf, sizeof(buf), "{\"id\":\"%s\",\"fw\":\"0.1.0\",\"mode\":%d,\"name\":\"%s\"}", settings_device_id(), g_cfg.mode, s_name);
        return os_mbuf_append(ctxt->om, buf, n) == 0 ? 0 : BLE_ATT_ERR_INSUFFICIENT_RES;
    }
    if (ctxt->op != BLE_GATT_ACCESS_OP_WRITE_CHR) return 0;
    static uint8_t buf[512 + 1];
    uint16_t len = 0;
    if (ble_hs_mbuf_to_flat(ctxt->om, buf, sizeof(buf) - 1, &len) != 0) return BLE_ATT_ERR_INVALID_ATTR_VALUE_LEN;
    buf[len] = 0;
    switch (which) {
    case C_AUDIO_DN:
        if (s_bridge_mode && s_audio_via_belt && len > 2) audio_rx(1, 0xF, buf + 2, len - 2);
        break;
    case C_CTRL_RX:
        if (s_bridge_mode) handle_ctrl((const char *)buf);
        break;
    case C_PROV:
        handle_prov((const char *)buf);
        break;
    }
    return 0;
}

static const struct ble_gatt_svc_def SVCS[] = {
    {
        .type = BLE_GATT_SVC_TYPE_PRIMARY,
        .uuid = &U_SVC.u,
        .characteristics = (struct ble_gatt_chr_def[]){
            {.uuid = &U_CTRL_RX.u, .access_cb = chr_access, .arg = (void *)C_CTRL_RX, .flags = BLE_GATT_CHR_F_WRITE},
            {.uuid = &U_CTRL_TX.u, .access_cb = chr_access, .arg = (void *)C_CTRL_TX, .flags = BLE_GATT_CHR_F_NOTIFY, .val_handle = &h_ctrl_tx},
            {.uuid = &U_AUDIO_UP.u, .access_cb = chr_access, .arg = (void *)C_AUDIO_UP, .flags = BLE_GATT_CHR_F_NOTIFY, .val_handle = &h_audio_up},
            {.uuid = &U_AUDIO_DN.u, .access_cb = chr_access, .arg = (void *)C_AUDIO_DN, .flags = BLE_GATT_CHR_F_WRITE | BLE_GATT_CHR_F_WRITE_NO_RSP},
            {.uuid = &U_PROV.u, .access_cb = chr_access, .arg = (void *)C_PROV, .flags = BLE_GATT_CHR_F_WRITE},
            {.uuid = &U_INFO.u, .access_cb = chr_access, .arg = (void *)C_INFO, .flags = BLE_GATT_CHR_F_READ},
            {0},
        },
    },
    {0},
};

// ---------------- GAP ----------------
static int gap_event(struct ble_gap_event *e, void *arg)
{
    switch (e->type) {
    case BLE_GAP_EVENT_CONNECT:
        if (e->connect.status == 0) {
            s_conn = e->connect.conn_handle;
            struct ble_gap_upd_params p = {.itvl_min = 6, .itvl_max = 12, .latency = 0, .supervision_timeout = 300};
            ble_gap_update_params(s_conn, &p); // 7.5-15 ms interval for audio
            ESP_LOGI(TAG, "connected");
            if (s_bridge_mode) {
                ui_lock(); g_ui.conn = CONN_CONNECTING; snprintf(g_ui.status, sizeof(g_ui.status), "APP LINKED"); ui_unlock();
                ui_changed();
            }
        } else {
            advertise();
        }
        break;
    case BLE_GAP_EVENT_DISCONNECT:
        ESP_LOGI(TAG, "disconnected");
        s_conn = BLE_HS_CONN_HANDLE_NONE;
        s_sub_ctrl = s_sub_audio = false;
        audio_set_transmit(false);
        if (s_bridge_mode) {
            ui_lock();
            g_ui.conn = CONN_OFFLINE;
            snprintf(g_ui.status, sizeof(g_ui.status), "PAIR WITH APP");
            for (int i = 0; i < 4; i++) g_ui.keys[i].talk = false;
            ui_unlock();
            ui_changed();
        }
        advertise();
        break;
    case BLE_GAP_EVENT_SUBSCRIBE:
        if (e->subscribe.attr_handle == h_ctrl_tx) {
            s_sub_ctrl = e->subscribe.cur_notify;
            if (s_sub_ctrl) {
                char buf[48];
                int n = snprintf(buf, sizeof(buf), "{\"t\":\"hello\",\"keys\":4,\"fw\":\"0.1.0\"}");
                notify(h_ctrl_tx, buf, n);
            }
        }
        if (e->subscribe.attr_handle == h_audio_up) s_sub_audio = e->subscribe.cur_notify;
        break;
    case BLE_GAP_EVENT_ADV_COMPLETE:
        advertise();
        break;
    }
    return 0;
}

static void advertise(void)
{
    if (!s_bridge_mode && esp_timer_get_time() > s_prov_until_us) {
        ui_lock(); g_ui.bt_setup = false; ui_unlock();
        return;
    }
    struct ble_hs_adv_fields f = {0};
    f.flags = BLE_HS_ADV_F_DISC_GEN | BLE_HS_ADV_F_BREDR_UNSUP;
    f.uuids128 = &U_SVC;
    f.num_uuids128 = 1;
    f.uuids128_is_complete = 1;
    ble_gap_adv_set_fields(&f);
    struct ble_hs_adv_fields r = {0};
    r.name = (uint8_t *)s_name;
    r.name_len = strlen(s_name);
    r.name_is_complete = 1;
    ble_gap_adv_rsp_set_fields(&r);
    struct ble_gap_adv_params p = {.conn_mode = BLE_GAP_CONN_MODE_UND, .disc_mode = BLE_GAP_DISC_MODE_GEN};
    int32_t dur = s_bridge_mode ? BLE_HS_FOREVER : (int32_t)((s_prov_until_us - esp_timer_get_time()) / 1000);
    ble_gap_adv_start(s_own_addr, NULL, dur, &p, gap_event, NULL);
}

static void on_sync(void)
{
    ble_hs_id_infer_auto(0, &s_own_addr);
    advertise();
}

static void host_task(void *arg)
{
    nimble_port_run();
    nimble_port_freertos_deinit();
}

static bool s_started;

static void start_stack(void)
{
    if (s_started) return;
    s_started = true;
    snprintf(s_name, sizeof(s_name), "OpenLine-%.4s", settings_device_id() + 8);
    ESP_ERROR_CHECK(nimble_port_init());
    ble_hs_cfg.sync_cb = on_sync;
    ble_svc_gap_init();
    ble_svc_gatt_init();
    ble_gatts_count_cfg(SVCS);
    ble_gatts_add_svcs(SVCS);
    ble_svc_gap_device_name_set(s_name);
    ble_att_set_preferred_mtu(247);
    nimble_port_freertos_init(host_task);
}

void ble_link_start_bridge(void)
{
    s_bridge_mode = true;
    ui_set_transport(&TRANSPORT);
    audio_init(on_mic, gain_unity);
    ui_lock(); snprintf(g_ui.status, sizeof(g_ui.status), "PAIR WITH APP"); ui_unlock();
    start_stack();
}

void ble_link_start_provisioning(int seconds)
{
    s_prov_until_us = esp_timer_get_time() + seconds * 1000000LL;
    ui_lock(); g_ui.bt_setup = true; ui_unlock();
    if (s_started) {
        if (s_conn == BLE_HS_CONN_HANDLE_NONE) { ble_gap_adv_stop(); advertise(); }
    } else {
        start_stack();
    }
}
