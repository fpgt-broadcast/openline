# OpenLine beltpack — hardware reference design

A low-cost beltpack with a 4-pin XLR headset jack, 4 talk keys with RGB LEDs, a volume/menu encoder and a mono screen. One PCB covers all variants:

| Variant | Connects via | Power | Screen | Target BOM (qty 100) |
|---|---|---|---|---|
| **OL-1** | Bluetooth LE to a phone/computer running the OpenLine app, **or** Wi‑Fi straight to the server | 18650 Li-ion + USB‑C charging | 2.42" OLED (SSD1309) | ~$30 |
| **OL-1E** | Same + **wired Ethernet** (W5500) | **PoE (802.3af)** and/or battery | 2.42" OLED | ~$44 |
| e-ink option | any | any | 2.13" e-ink (SSD1680), +~$6 | |

The firmware is in [`../firmware`](../firmware). **3D-printed case, belt clip, lit key caps and charging dock:** [`case/`](case/README.md) (rev D: 2.42" screen, 4 lit keys and the volume knob on the top face, built-in 18650). Variant options are `idf.py menuconfig` → *OpenLine beltpack*.

> Status: this is a reference design and pin map, not a fabricated, validated PCB. The firmware compiles but has not been run on hardware yet. Build the dev-board prototype below first to bring up the codec, display and keys, then lay out the PCB from this document.

## Block diagram

```
                ┌──────────────── ESP32-S3-WROOM-1-N16R8 ────────────────┐
 XLR-4M ─ RF/ESD ─ WM8960 codec ── I2S (MCLK/BCLK/WS/DIN/DOUT) ─┤  Wi-Fi + BLE 5 (on module)
 headset   filter  (mic PGA+boost,     I2C ──┬─────────────────┤
                    HP amp, sidetone)        └─ SSD1309 OLED   │  USB-C (flash/console) ── USB D+/D−
                                                                │
 4 talk keys + EC11 encoder ────────────────── GPIO ────────────┤
 4× WS2812 (under keys) ────────────────────── GPIO47 ──────────┤
 W5500 + PoE MagJack (OL-1E) ───────────────── SPI2 ────────────┤
 e-ink (option) ────────────────────────────── SPI2 ────────────┘

 USB-C 5V ─┐
 PoE 5V  ──┴─► BQ24075 charger/power-path ─► 18650 ─► TPS63001 buck-boost ─► 3.3 V rail
```

## Headset connector (standard intercom headset)

Beltpack side: **Neutrik NC4MD-L-1 (XLR-4 male)** — accepts the XLR-4F cable found on Clear-Com / RTS / Telex / Beyerdynamic single-muff intercom headsets.

| XLR-4 pin | Signal | Circuit |
|---|---|---|
| 1 | Mic common (−) / shield | to WM8960 `LINPUT1` (inverting) via 1 µF, and to AGND through 1 kΩ |
| 2 | Mic hot (+) | to WM8960 `LINPUT2` (non-inverting) via 1 µF; 2.2 kΩ to `MICBIAS` (bias only enabled for electret, set in menu/admin) |
| 3 | Headphone common (−) | AGND |
| 4 | Headphone hot (+) | `LOUT1` → 220 µF → 10 Ω → pin 4 (`ROUT1` driven with the same mono signal; tie to pin 4 through 10 Ω for low-impedance headsets) |

Notes:
- **RF filtering is mandatory** next to a Wi‑Fi radio: ferrite bead + 100 pF to ground on pins 1, 2 and 4 at the connector, and keep the mic traces short and differential.
- **ESD**: TVS array (e.g. TPD4E05U06) on all four pins.
- **Gain**: dynamic intercom mics are around −55 dBV/Pa. The firmware uses +29 dB boost + up to +30 dB PGA for *Dynamic*, and +13 dB with bias for *Electret*.
- **Headset detect** (`HS_SENSE`, GPIO2): 100 kΩ from 3.3 V to mic hot, and the same node to the ADC through 10 kΩ. With no capsule (>10 kΩ) the node reads high; a 150–600 Ω dynamic capsule pulls it below 2.5 V. The DC is blocked from the codec by its 1 µF caps.
- 5-pin stereo headsets (XLR-5) work with an adapter (pins 4+5 → 4).
- Optional: a 3.5 mm TRRS jack in parallel for phone-style headsets (CTIA pinout), with the bias enabled.

## Pin map (ESP32-S3-WROOM-1-N16R8)

Same as [`firmware/main/board.h`](../firmware/main/board.h).

| GPIO | Function | | GPIO | Function |
|---|---|---|---|---|
| 1 | VBAT sense (ADC, 100k/100k divider) | | 14 | W5500 CS |
| 2 | Headset sense (ADC) | | 15 | W5500 INT |
| 3 | e-ink BUSY | | 16 | W5500 RST |
| 4–7 | Talk keys 1–4 (to GND, internal pull-up) | | 17/18 | Encoder A/B |
| 8 | I2C SDA (codec 0x1A, OLED 0x3C), 2.2k pull-up | | 21 | Encoder push |
| 9 | I2C SCL | | 38 | I2S MCLK (4.096 MHz) |
| 10 | e-ink CS | | 39 | I2S BCLK |
| 11 | SPI MOSI | | 40 | I2S WS |
| 12 | SPI SCK | | 41 | I2S DOUT → codec DACDAT |
| 13 | SPI MISO | | 42 | I2S DIN ← codec ADCDAT |
| 43 | e-ink DC | | 44 | Charger PG/STAT (low = external power) |
| 47 | WS2812 data (4 LEDs) | | 48 | e-ink RST |
| 19/20 | USB D−/D+ (USB-C, console + flashing) | | 0 | BOOT button |

Avoid GPIO 26–37 (flash/octal PSRAM) and 45/46 (strapping).

## Controls (matches the "Beltpack 3" screen design)

- **Talk key 1–4**: tap = latch talk, hold = momentary talk, tap again to unlatch. The LED under the key shows the channel colour: dim = listening, half = someone talking, full = you're talking, flashing white = incoming call.
- **Encoder**: turn = volume of the last key touched, press = listen on/off, double press = call that channel, hold = menu (mic type, mic gain, sidetone, headphone level, brightness, info, Bluetooth setup, mode, reboot).
- Hold the encoder while powering on to force Bluetooth setup.

## Power

- **Charger / power path:** BQ24075 (USB‑C 5 V or PoE 5 V in, runs from external power while charging). `PGOOD` goes to GPIO44.
- **3.3 V:** TPS63001 buck-boost (runs the whole 18650 range at 800 mA peak). An AP2112K LDO works if you accept losing the last ~10 % of the battery.
- **Battery:** 1× 18650, 3000 mAh. Estimated current draw:

| Mode | Current | Runtime on 3000 mAh |
|---|---|---|
| Bluetooth to phone, beltpack audio | ~70 mA | ~35 h |
| Bluetooth, remote-only (audio on phone/AirPods) | ~45 mA | ~55 h |
| Wi‑Fi standalone (power save off for latency) | ~150 mA | ~18 h |
| Ethernet + PoE | from PoE | — |

- **PoE (OL-1E):** Silvertel **Ag9905MT** (802.3af, isolated, 5 V 1.8 A). A cheaper alternative is SDAPO DP9905. Use a PoE MagJack with centre taps (e.g. HanRun HR911130C), with the W5500 at 20 MHz SPI.

## BOM (OL-1E, indicative prices at qty 100)

| Part | Example | ~$ |
|---|---|---|
| MCU module | ESP32-S3-WROOM-1-N16R8 | 3.60 |
| Audio codec | WM8960CGEFL | 1.80 |
| OLED 2.42" 128×64 I2C (4-pin) | SSD1309 module (1.3" SH1106 ≈ $2.20 also works) | 4.50 |
| XLR-4 male panel | Neutrik NC4MD-L-1 (or Amphenol AX4M) | 3.50 |
| Keys | 4× Kailh choc / 12 mm tactile with caps | 1.60 |
| RGB LEDs | 4× WS2812B-2020 | 0.30 |
| Encoder | EC11 with switch | 0.40 |
| Charger | BQ24075RGTR | 1.10 |
| 3.3 V buck-boost | TPS63001DRCR + 1.5 µH | 1.60 |
| USB-C receptacle + 5.1k CC resistors | | 0.30 |
| 18650 holder + cell | | 3.50 |
| Ethernet | W5500 + 25 MHz xtal | 1.50 |
| PoE MagJack | HR911130C | 1.40 |
| PoE module | Ag9905MT (or DP9905 ≈ $3) | 9.00 |
| ESD / RF filtering / passives | TPD4E05U06, beads, caps | 1.20 |
| PCB 4-layer + assembly | | 5.00 |
| Enclosure + belt clip | 3D printed / SLA | 4.00 |
| **Total OL-1E** | | **~$44** |
| **OL-1** (no W5500 / MagJack / PoE) | | **~$30** |

For the cheapest build, leave out the PoE parts (OL-1), use an LDO instead of the buck-boost, and keep the OLED. E-ink adds about $6 and makes the screen readable in daylight with near-zero display power. Its slower refresh (about 0.3 s) is acceptable because the key LEDs show talk and activity instantly.

## Prototype from off-the-shelf boards (about $60, no PCB)

1. **ESP32-S3-DevKitC-1-N16R8**
2. **SparkFun WM8960 Audio Codec Breakout** (Qwiic): wire I2C to GPIO 8/9, and I2S MCLK/BCLK/WS/DIN/DOUT to GPIO 38–42 as in the pin map. Use its 3.5 mm input/output to start with, then add the XLR-4.
3. **2.42" SSD1309 I2C OLED** (the 4-pin I2C version) on the same I2C bus. The 1.3" SH1106 also works: pick it in menuconfig.
4. **4 arcade/tactile buttons** on GPIO 4–7 to GND, **EC11 encoder** on 17/18/21, and a **4-LED WS2812 strip** on GPIO 47 (5 V).
5. **Neutrik NC4MD-L-1** wired as in the table above, through 1 µF caps to the codec breakout's mic input and 220 µF on the headphone output.
6. Ethernet/PoE: a **W5500 mini module** on SPI (GPIO 11–16) plus a **5 V PoE splitter** into the DevKit USB port.

Flash it with ESP-IDF 5.3 or 5.4:

```bash
cd firmware
git clone --depth 1 -b v1.5.2 https://github.com/xiph/opus components/opus/libopus
idf.py set-target esp32s3 && idf.py menuconfig && idf.py build flash monitor
```

Then pair it from the OpenLine app (**Menu → Connect beltpack**), or provision it for Wi‑Fi/Ethernet from **Admin → Beltpacks → Set up beltpack via Bluetooth**.

## Bring-up checklist

- [ ] `wm8960: initialised` in the log (I2C acks at 0x1A).
- [ ] OLED shows `BT SETUP`.
- [ ] Keys and encoder change the screen. LEDs light in channel colours once paired.
- [ ] Sidetone: you hear yourself immediately when you speak (analog path, no latency).
- [ ] Talk to a browser client: clean audio, no buzz with Wi‑Fi active (if there is buzz, add the RF filter caps).
- [ ] Battery % tracks a bench supply sweep from 3.3 to 4.2 V.
- [ ] PoE: the board boots from a PoE switch with no battery, and admin shows `ETHERNET + PoE`.
- [ ] e-ink (if fitted): image orientation is correct (otherwise toggle *Rotate e-ink 180°*), and partial refresh doesn't ghost; firmware does a full refresh every 30 updates.
