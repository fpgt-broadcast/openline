# Beltpack firmware (ESP-IDF)

The target is ESP32-S3 (ESP32-S3-WROOM-1-N16R8), built with ESP-IDF **5.3 or 5.4**. See [`../hardware/README.md`](../hardware/README.md) for wiring.

```bash
. $IDF_PATH/export.sh
git clone --depth 1 -b v1.5.2 https://github.com/xiph/opus components/opus/libopus   # once
idf.py set-target esp32s3
idf.py menuconfig     # OpenLine beltpack → display type (OLED / e-ink), Ethernet fitted
idf.py build flash monitor
```

`esp_websocket_client` and `led_strip` (v3) come from the ESP component registry on the first build. libopus is built from upstream source by `components/opus`.

**Building without access to the component registry:** clone the two components from GitHub, then turn the component manager off:

```bash
git clone --depth 1 -b websocket-v1.8.0 https://github.com/espressif/esp-protocols /opt/esp/extra/ep
git clone --depth 1 https://github.com/espressif/idf-extra-components /opt/esp/extra/iec
IDF_COMPONENT_MANAGER=0 idf.py -DEXTRA_COMPONENT_DIRS="/opt/esp/extra/iec/led_strip;/opt/esp/extra/ep/components/esp_websocket_client" build
```

> **Status:** compiles cleanly with ESP-IDF v5.3.2 (OLED + Ethernet and e-ink variants, no warnings; image ≈1.5 MB of the 3 MB app partition). It has **not been run on hardware yet**. Bring it up with the checklist in the hardware README.

## Modules

| File | Purpose |
|---|---|
| `main.c` | Boot and mode selection (Bluetooth bridge / Wi‑Fi / Ethernet / Bluetooth setup) |
| `audio.c` | I2S at 16 kHz, Opus 20 ms encode/decode, up to 4 simultaneous talkers with jitter buffers |
| `codec_wm8960.c` | Headset mic (dynamic/electret), headphone level, zero-latency analog sidetone |
| `ws_link.c` | Standalone mode: server protocol, telemetry, remote control, assign/netcfg/settings |
| `ble_link.c` | Phone bridge mode (keys/screen + optional audio) and BLE provisioning |
| `net.c` | Wi‑Fi STA or W5500 Ethernet, DHCP or static IP |
| `ui.c` | Key logic (tap = latch, hold = momentary), encoder, menu, screen layout, LEDs |
| `display.c` | 1-bit framebuffer, SSD1309 (2.42") / SH1106 / SSD1306 OLED, SSD1680 e-ink |
| `keys.c`, `power.c`, `settings.c` | Buttons/encoder/WS2812, battery/charger/headset sense, NVS settings |
| `tools/genfont.py` | Regenerates `main/fonts.h` from a TTF |

## First setup

1. On first boot the screen shows `BT SETUP`.
2. Either pair from the app (**Menu → Connect beltpack**) to use it over Bluetooth, or go to **Admin → Beltpacks → Set up beltpack via Bluetooth** and enter Wi‑Fi or Ethernet details plus the server URL.
3. If you didn't pick a user during setup, turn on **Admin → Beltpacks → Accept new beltpacks**: the beltpack shows up there. Pick a user, and it receives that user's token and reconnects.

To get back into setup, hold the encoder while powering on, or use **Menu → BT SETUP**.
