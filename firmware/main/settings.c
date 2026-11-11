#include "settings.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "esp_log.h"
#include "esp_mac.h"
#include "nvs.h"
#include "nvs_flash.h"

static const char *TAG = "settings";
#define SETTINGS_VERSION 1

ol_settings_t g_cfg;

static void defaults(ol_settings_t *s)
{
    memset(s, 0, sizeof(*s));
    s->version = SETTINGS_VERSION;
    s->mode = MODE_BLE;
    s->dhcp = 1;
    s->mic_gain = 70;
    s->sidetone = 20;
    s->hp_level = 80;
    s->bright = 80;
}

void settings_load(void)
{
    esp_err_t err = nvs_flash_init();
    if (err == ESP_ERR_NVS_NO_FREE_PAGES || err == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        nvs_flash_erase();
        nvs_flash_init();
    }
    defaults(&g_cfg);
    nvs_handle_t h;
    if (nvs_open("openline", NVS_READONLY, &h) == ESP_OK) {
        size_t len = sizeof(g_cfg);
        ol_settings_t tmp;
        if (nvs_get_blob(h, "cfg", &tmp, &len) == ESP_OK && len == sizeof(tmp) && tmp.version == SETTINGS_VERSION) {
            g_cfg = tmp;
        }
        nvs_close(h);
    }
    ESP_LOGI(TAG, "mode=%d server=%s ssid=%s token=%s", g_cfg.mode, g_cfg.server, g_cfg.ssid, g_cfg.token[0] ? "set" : "none");
}

void settings_save(void)
{
    nvs_handle_t h;
    if (nvs_open("openline", NVS_READWRITE, &h) != ESP_OK) return;
    nvs_set_blob(h, "cfg", &g_cfg, sizeof(g_cfg));
    nvs_commit(h);
    nvs_close(h);
}

bool settings_is_network_configured(void)
{
    if (!g_cfg.server[0]) return false;
    if (g_cfg.mode == MODE_WIFI) return g_cfg.ssid[0] != 0;
    return g_cfg.mode == MODE_ETH;
}

#define SETSTR(field)                                       \
    do {                                                    \
        strncpy(s->field, val, sizeof(s->field) - 1);       \
        s->field[sizeof(s->field) - 1] = 0;                 \
        return true;                                        \
    } while (0)

static uint8_t pct(const char *v)
{
    int n = atoi(v);
    return n < 0 ? 0 : n > 100 ? 100 : (uint8_t)n;
}

bool settings_set_kv(ol_settings_t *s, const char *key, const char *val)
{
    if (!strcmp(key, "mode")) {
        if (!strcmp(val, "wifi")) s->mode = MODE_WIFI;
        else if (!strcmp(val, "ethernet")) s->mode = MODE_ETH;
        else s->mode = MODE_BLE;
        return true;
    }
    if (!strcmp(key, "ssid")) SETSTR(ssid);
    if (!strcmp(key, "pass")) SETSTR(pass);
    if (!strcmp(key, "token")) SETSTR(token);
    if (!strcmp(key, "name")) SETSTR(name);
    if (!strcmp(key, "ip")) SETSTR(ip);
    if (!strcmp(key, "mask")) SETSTR(mask);
    if (!strcmp(key, "gw") || !strcmp(key, "gateway")) SETSTR(gw);
    if (!strcmp(key, "dns")) SETSTR(dns);
    if (!strcmp(key, "server")) {
        // accept https://host, host, wss://host/ws
        char buf[160];
        const char *v = val;
        const char *scheme = "wss://";
        if (!strncmp(v, "https://", 8)) v += 8;
        else if (!strncmp(v, "http://", 7)) { v += 7; scheme = "ws://"; }
        else if (!strncmp(v, "wss://", 6)) v += 6;
        else if (!strncmp(v, "ws://", 5)) { v += 5; scheme = "ws://"; }
        snprintf(buf, sizeof(buf), "%s%s", scheme, v);
        size_t n = strlen(buf);
        while (n && buf[n - 1] == '/') buf[--n] = 0;
        if (n < 3 || strcmp(buf + n - 3, "/ws")) strncat(buf, "/ws", sizeof(buf) - n - 1);
        strncpy(s->server, buf, sizeof(s->server) - 1);
        return true;
    }
    if (!strcmp(key, "dhcp")) { s->dhcp = (!strcmp(val, "1") || !strcmp(val, "true")); return true; }
    if (!strcmp(key, "micType")) { s->mic_electret = !strcmp(val, "electret"); return true; }
    if (!strcmp(key, "micGain")) { s->mic_gain = pct(val); return true; }
    if (!strcmp(key, "sidetone")) { s->sidetone = pct(val); return true; }
    if (!strcmp(key, "hpLevel")) { s->hp_level = pct(val); return true; }
    if (!strcmp(key, "bright")) { s->bright = pct(val); return true; }
    return false;
}

const char *settings_device_id(void)
{
    static char id[13];
    if (!id[0]) {
        uint8_t mac[6];
        esp_read_mac(mac, ESP_MAC_WIFI_STA);
        snprintf(id, sizeof(id), "%02x%02x%02x%02x%02x%02x", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]);
    }
    return id;
}
