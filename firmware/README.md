# Beltpack firmware (ESP-IDF)

The target is ESP32-S3 (ESP32-S3-WROOM-1-N16R8), built with ESP-IDF **5.3 or 5.4**. See [`../hardware/README.md`](../hardware/README.md) for wiring.

```bash
. $IDF_PATH/export.sh
idf.py set-target esp32s3
idf.py menuconfig     # OpenLine beltpack → display type (OLED / e-ink), Ethernet fitted
idf.py build flash monitor
```

Managed components (`main/idf_component.yml`) are fetched on the first build: `esp_websocket_client`, `led_strip`, and a libopus port (`78/esp-opus`). The code only uses the standard `<opus.h>` API, so if that component isn't available, any other libopus port for ESP-IDF works.

> This firmware has **not been compiled or tested on hardware yet**. Expect small API fixes on the first build, and verify the codec, display and e-ink with the bring-up checklist in the hardware README.

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
| `display.c` | 1-bit framebuffer, SH1106/SSD1306 OLED, SSD1680 e-ink |
| `keys.c`, `power.c`, `settings.c` | Buttons/encoder/WS2812, battery/charger/headset sense, NVS settings |
| `tools/genfont.py` | Regenerates `main/fonts.h` from a TTF |

## First setup

1. On first boot the screen shows `BT SETUP`.
2. Either pair from the app (**Menu → Connect beltpack**) to use it over Bluetooth, or go to **Admin → Beltpacks → Set up beltpack via Bluetooth** and enter Wi‑Fi or Ethernet details plus the server URL.
3. A beltpack without a user appears in Admin as *unassigned*. Pick a user there, and the beltpack receives that user's token and reconnects.

To get back into setup, hold the encoder while powering on, or use **Menu → BT SETUP**.
