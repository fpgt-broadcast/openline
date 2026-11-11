#pragma once
#include <stdbool.h>
#include <stdint.h>

typedef enum { MODE_BLE = 0, MODE_WIFI = 1, MODE_ETH = 2 } ol_mode_t;

typedef struct {
    uint8_t version;
    uint8_t mode; // ol_mode_t
    char ssid[33];
    char pass[65];
    char server[128]; // wss://host/ws
    char token[65];
    char name[24];
    uint8_t dhcp;
    char ip[16], mask[16], gw[16], dns[16];
    uint8_t mic_electret; // 0 dynamic, 1 electret (mic bias on)
    uint8_t mic_gain;     // 0..100
    uint8_t sidetone;     // 0..100
    uint8_t hp_level;     // 0..100
    uint8_t bright;       // 0..100
} ol_settings_t;

extern ol_settings_t g_cfg;

void settings_load(void);
void settings_save(void);
bool settings_is_network_configured(void);
// Applies one key/value (from BLE provisioning or server netcfg). Returns false if unknown.
bool settings_set_kv(ol_settings_t *s, const char *key, const char *val);
const char *settings_device_id(void); // lowercase hex MAC, 12 chars
