# OpenLine: notes for Claude

OpenLine is a self-hosted, open-source alternative to Unity Intercom, plus a low-cost hardware beltpack inspired by Green-GO. The owner runs live productions (vMix, NDI, Cloudflare). Keep explanations plain and practical: they build and buy the hardware themselves and often send screenshots of AliExpress listings to check.

## How we work

- **Never merge a PR until the owner says "merge it"** (or "merge both"). Open a PR for every change; pushing a branch alone isn't done.
- PR descriptions: what changed in plain words, then a **Tested** section saying what was actually run and what wasn't (e.g. "not tested on real hardware").
- Commit as `git -c user.name="OpenLine" -c user.email="openline@localhost" commit …`. Never put model names or IDs in commits, PRs or code.
- Releases are cut by pushing a `v*` tag (pre-releases as `vX.Y.Z-beta`); `.github/workflows/release.yml` builds everything. Ask the owner before tagging.
- Test UI changes in a real browser (Playwright + the preinstalled Chromium) against a running server, at iPhone size too. The owner uses **Safari on iPhone**, so avoid `confirm()`/`alert()` (use the two-tap `sure()` buttons) and check Safari-specific behaviour.
- The repo moved from `DueHack12/openline` to **`fpgt-broadcast/openline`** (private).

## Layout

| Path | What |
|---|---|
| `cmd/openline-server`, `internal/server` | Go server: WebSocket hub (SFU-style, no mixing → mix-minus for free), REST admin API, config store (`data/config.json`), shows, channel outputs, tally routing |
| `internal/proto` | Wire protocol (JSON control + binary Opus frames). Spec in `docs/PROTOCOL.md` |
| `web/static` | Browser app (`index.html`/`app.js`), full admin (`admin.html`/`admin.js`), **phone admin** (`m.html`/`m.js`), Web Bluetooth beltpack link (`ble.js`) |
| `cmd/openline-bridge`, `internal/bridge` | On-site bridge PC: links (NDI in/out, **sound card in/out**, test tone), channel outputs (one NDI source and/or sound card output per channel), vMix tally |
| `internal/ndi`, `internal/opus`, `internal/portaudio`, `internal/dl` | Native libraries loaded at runtime with purego (no cgo): NDI runtime, libopus, PortAudio |
| `firmware/` | ESP32-S3 beltpack firmware (ESP-IDF 5.3) |
| `hardware/README.md`, `hardware/case/` | Beltpack design notes, BOM, OpenSCAD case (rev D), dock, PCB outlines, main-board layout |
| `deploy/`, `Dockerfile`, `docs/DEPLOY.md` | Docker compose (server + Cloudflare tunnel), systemd unit |
| `scripts/package-release.sh`, `packaging/windows/` | Release zips (Windows bridge zip includes `opus.dll` + `portaudio.dll`) |

## Build and test

```bash
go vet ./... && go test ./...      # all Go tests (bridge tests need libopus: apt install libopus0)
make build                          # binaries into bin/
```

Default server port is **8999** (the owner has other software on 8080). `-self-signed` gives LAN HTTPS. First run prints the admin password; demo users Director/1111 (admin), Camera 1/2222, A1/3333.

Firmware (`firmware/README.md` has details): ESP-IDF v5.3.2. In cloud sessions the component registry is blocked, so build with `IDF_COMPONENT_MANAGER=0`, `OPUS_SRC_DIR=<libopus source>` and `EXTRA_COMPONENT_DIRS` pointing at `led_strip` and `esp_websocket_client` cloned from GitHub. The Python env needed `--no-constraints` plus an empty constraints file and `idf-component-manager~=2.0`.

Case: OpenSCAD 2021.01 (`apt install openscad`), `hardware/case/build.sh` exports STLs, `render.sh` renders PNGs (run under `xvfb-run`). Check meshes are watertight (trimesh).

PortAudio testing without a sound card: define ALSA `type file` PCMs in `~/.asoundrc` (`infile` for input). Use **separate** PCMs for input and output (the file plugin mirrors capture into its output file). Virtual devices don't pace, so the bridge caps both directions at 1 s ahead of real time; delete big test files afterwards.

## Product decisions already made

- App UI "design D" with a big PTT; tap a channel for a detail sheet (levels, who's talking/listening).
- Users have an **assignment** (login, e.g. "Camera 1") and a **person** name ("John Doe"); vMix tally input numbers per user; optional full-screen tally view (admin-switchable).
- **Shows** = separate sets of channels and users for running a second show at the same time (Unity calls these groups). Old configs migrate into one "Main" show. Names only need to be unique within a show; same name + PIN across shows is rejected.
- **Channel outputs**: per channel, NDI and/or "Card out N", played by a bridge login with the **channel outputs** role (`outputs` section in `bridge.json`).
- Admin Channels tab numbers channels by position; internal ids never change (users, beltpacks, bridge configs refer to them).

## Hardware status

- Beltpack: ESP32-S3-DevKitC-1 N16R8, SparkFun-style WM8960 board (BOB-21250: own 24 MHz crystal, no MCLK; firmware default "Codec clock → codec's own crystal", ADCLRC unconnected), 2.42" SSD1309 I2C OLED (firmware default; "Rotate OLED 180°" option), 6×6×5 mm switches, SK6812MINI-E key LEDs (WS2812B strip only for breadboard), EC11 with 20 mm knurled shaft, XLR-4 male, Type-C charge/boost module + 1-slot 18650 holder + Samsung 35E, USR-ES1 W5500 + external USB-C PoE splitter, SS12D00 power switch, 220 µF 10 V + 1 µF film caps.
- Parts for **one** prototype are ordered. **Firmware has never run on hardware.**
- Case rev D (`hardware/case`): mockup layout on the **top face** (2.42" screen at the far edge, 4 lit keys under it, knob at the end of the row, wearer's view from the belt side). Bottom: XLR-4, **CHG** USB-C (charge module, dock, PoE splitter) and **USB** USB-C (DevKit flashing). RJ45 on the right wall. 90 × 66 × 102 mm.
- PCBs (PCBWay): two boards, a main board (upright, modules in sockets) and a control board (under the lid). Only outlines and `main_board_layout.svg` exist. **Next step:** when the parts arrive, get caliper measurements (OLED size/window/pins, charge module pads and USB-C position, USR-ES1 pins and jack offset, WM8960 and DevKit pin-row spacing, battery holder size), update `params.scad`, then draw both boards in KiCad and give the owner Gerbers + PCBWay settings. Don't order PCBs before that.

## Open items

1. **Open the PR for branch `card-links`** ("Bridge: sound card links (in and out), e.g. Saramonic WiTalk9"). It's pushed and tested (unit test plus an end-to-end PortAudio run with virtual ALSA devices: no echo, real-time pacing) but not on real hardware.
2. **Saramonic WiTalk9** (the owner has the headset kit, no base): link it through the master or remote headset's TRRS jack with the **WiTalk9 Link cable kit**. Turn on link mode by holding ENC 3 s (mic light solid purple; off after every power-up), keep sidetone off. Likely needs Saramonic's own cable (probably crossed over, since it also cascades two systems), not a generic TRRS or 3-pole aux cable. Setup is in `docs/BRIDGE.md` → "Sound card links".
3. **Next release** (e.g. `v0.1.1-beta`, ask first): v0.1.0-beta firmware still defaults to the 1.3" SH1106 and ESP32-driven MCLK; the bridge zip gains `portaudio.dll`, channel outputs and card links.
4. Untested: NDI outputs against a real NDI runtime; physical sound cards; all beltpack hardware.

## Owner's setup

- Server runs **off-site in OrbStack on a Mac** (`deploy/docker-compose.yml`, Cloudflare tunnel → `http://openline:8999`). Updates: replace the code keeping `deploy/data`, then `docker compose up -d --build`.
- The bridge runs on a **Windows PC at the venue** (NDI program/announcer feeds, vMix tally, and now sound card links).
- Phone admin: `/m.html`.
- The beltpack build guide is a claude.ai artifact: https://claude.ai/artifact/Hu4TmQ7SRq2mHFrXhsCX6i (wiring, parts list, case section). Read it before updating it.
