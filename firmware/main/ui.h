#pragma once
#include <stdbool.h>
#include <stdint.h>

typedef struct {
    bool present;
    int ch_id;
    char name[16];
    uint32_t color; // 0xRRGGBB
    bool can_talk, can_listen;
    bool talk, listen;
    bool active;  // someone else is talking here
    bool call_in; // someone is calling this channel
    uint8_t vol;  // 0..100, 75 = unity
} ui_key_t;

typedef enum { CONN_OFFLINE = 0, CONN_CONNECTING, CONN_ONLINE } ui_conn_t;

typedef struct {
    ui_key_t keys[4];
    char user[24];
    char status[24]; // short status line when offline ("NO WIFI", "UNASSIGNED")
    ui_conn_t conn;
    bool muted;     // mic muted by admin
    bool bt_setup;  // BLE provisioning window open
    int selected;   // key the encoder acts on
    int64_t identify_until_us;
    int64_t overlay_until_us;
    char overlay[24];
    int battery;    // %, -1 = external power
    bool charging;
    bool headset;
    int rssi;
    char ip[16];
} ui_state_t;

// Transport (server websocket or BLE app link) implements these.
typedef struct {
    void (*talk)(int key, bool on);
    void (*listen)(int key, bool on);
    void (*call)(int key, bool on);
    void (*vol)(int key, int v); // NULL = volume is applied locally
} ui_transport_t;

extern ui_state_t g_ui;

void ui_init(void);
void ui_set_transport(const ui_transport_t *t);
void ui_lock(void);
void ui_unlock(void);
void ui_changed(void);
void ui_overlay(const char *text, int ms);

// input events (from keys.c)
void ui_key_event(int key, bool down);
void ui_enc_turn(int delta);
void ui_enc_button(bool down);

// network-mode mixer gain for a channel mask (bit i = user's channel i = key i)
float ui_gain_for_mask(uint32_t mask);
bool ui_any_talk(void);
