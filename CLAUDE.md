# OpenLine: notes for Claude

OpenLine is a self-hosted, open-source alternative to Unity Intercom, plus a low-cost hardware beltpack inspired by Green-GO. The owner runs live productions (vMix, NDI, Cloudflare). Keep explanations plain and practical: they build and buy the hardware themselves and often send screenshots of AliExpress listings to check.

## How we work

- **Never merge a PR until the owner says "merge it"** (or "merge both"). Open a PR for every change; pushing a branch alone isn't done.
- PR descriptions: what changed in plain words, then a **Tested** section saying what was actually run and what wasn't (e.g. "not tested on real hardware").
- Commit as `git -c user.name="OpenLine" -c user.email="openline@localhost" commit …`. Never put model names or IDs in commits, PRs or code.
- Releases: a `v*` tag builds everything (`.github/workflows/release.yml`). Cloud sessions can't push tags, so the owner publishes them in GitHub → Releases (tag on `main`, pre-release ticked). Tags so far: `v0.1.0-beta`, `v0.2.0-beta.1`, `v0.3.0-beta1`, `v0.3.0-beta2`, `v1.0.0`. Before a release, set the `VERSION` file to the new tag: Docker builds (the owner's server) read it; other builds use `git describe`. The version shows in the app Menu, admin → Server and `/api/info`.
- Docs stay short and plain: README, `docs/DEPLOY.md`, `docs/BRIDGE.md`, `docs/PROTOCOL.md`. Update them with every user-visible change.
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

Default server port is **8999** (the owner has other software on 8080). `-self-signed` gives LAN HTTPS. First run prints the admin password and the starter users (Director = admin, Camera 1, A1) with **random 6-digit PINs**; tests pin them to 1111/2222/3333 (`setTestPINs`). Admin-page sign-in with a PIN needs 6+ digits, so in browser tests sign in to admin with the printed password.

Firmware (`firmware/README.md` has details): ESP-IDF v5.3.2. In cloud sessions the component registry is blocked, so build with `IDF_COMPONENT_MANAGER=0`, `OPUS_SRC_DIR=<libopus source>` and `EXTRA_COMPONENT_DIRS` pointing at `led_strip` and `esp_websocket_client` cloned from GitHub. The Python env needed `--no-constraints` plus an empty constraints file and `idf-component-manager~=2.0`.

Case: OpenSCAD 2021.01 (`apt install openscad`), `hardware/case/build.sh` exports STLs, `render.sh` renders PNGs (run under `xvfb-run`). Check meshes are watertight (trimesh).

PortAudio testing without a sound card: define ALSA `type file` PCMs in `~/.asoundrc` (`infile` for input). Use **separate** PCMs for input and output (the file plugin mirrors capture into its output file). Virtual devices don't pace, so the bridge caps both directions at 1 s ahead of real time; delete big test files afterwards.

## Product decisions already made

- App UI "design D" with a big PTT; tap a channel for a detail sheet (levels, who's talking/listening).
- Users have an **assignment** (login, e.g. "Camera 1") and a **person** name ("John Doe"); vMix tally input numbers per user; optional full-screen tally view (admin-switchable).
- **Shows** = separate sets of channels and users for running a second show at the same time (Unity calls these groups). Old configs migrate into one "Main" show. Names only need to be unique within a show; same name + PIN across shows is rejected.
- **Channel outputs**: per channel, NDI and/or "Card out N", played by a bridge login with the **channel outputs** role (`outputs` section in `bridge.json`).
- Admin Channels tab numbers channels by position; internal ids never change (users, beltpacks, bridge configs refer to them).
- **Background audio** (Menu, on by default on touch devices): the mix plays through a hidden `<audio>` (MediaStream) plus a looping `silence.wav` so iOS treats it as now playing; the AudioContext is resumed when iOS pauses it. Owner-tested on iPhone: listening works locked and in other apps, talking works from other apps, a locked iPhone mutes the mic (can't be fixed on the web). The lock-screen card shows status / role · person / server · show and the logo. A "Background log" in Menu helps debug.
- **Security model** (public server behind Cloudflare): per-IP (IPv6: per /64) and per-name login limits using `CF-Connecting-IP` from `-trusted-proxy cloudflared` (so admin Live shows real visitor IPs, often IPv6 / iCloud Private Relay); admin PIN sign-in needs 6+ digits; unassigned beltpacks only connect during the admin's "Accept new beltpacks" window and live in memory until adopted; Wi-Fi settings only go to a beltpack signed in with its user's token; CSP / no framing; admin token only in the Authorization header. Compose publishes 8999 on 127.0.0.1 only.
- Logo: `web/static/icon.svg` (owner is happy with it), PNGs `icon-192/512.png`, `apple-touch-icon.png`.

## Hardware status

- Beltpack: ESP32-S3-DevKitC-1 N16R8, SparkFun-style WM8960 board (BOB-21250: own 24 MHz crystal, no MCLK; firmware default "Codec clock → codec's own crystal", ADCLRC unconnected), 2.42" SSD1309 I2C OLED (firmware default; "Rotate OLED 180°" option), 6×6×5 mm switches, SK6812MINI-E key LEDs (WS2812B strip only for breadboard), EC11 with 20 mm knurled shaft, XLR-4 male, Type-C charge/boost module + 1-slot 18650 holder + Samsung 35E, USR-ES1 W5500 + external USB-C PoE splitter, SS12D00 power switch, 220 µF 10 V + 1 µF film caps.
- Parts for **one** prototype are ordered. **Firmware has never run on hardware.**
- Case rev D (`hardware/case`): mockup layout on the **top face** (2.42" screen at the far edge, 4 lit keys under it, knob at the end of the row, wearer's view from the belt side). Bottom: XLR-4, **CHG** USB-C (charge module, dock, PoE splitter) and **USB** USB-C (DevKit flashing). RJ45 on the right wall. 90 × 66 × 102 mm.
- PCBs (PCBWay): two boards, a main board (upright, modules in sockets) and a control board (under the lid). Only outlines and `main_board_layout.svg` exist. **Next step:** when the parts arrive, get caliper measurements (OLED size/window/pins, charge module pads and USB-C position, USR-ES1 pins and jack offset, WM8960 and DevKit pin-row spacing, battery holder size), update `params.scad`, then draw both boards in KiCad and give the owner Gerbers + PCBWay settings. Don't order PCBs before that.

## Open items (after v1.0)

v1.0 = server, app and bridge; the beltpack is **experimental**. v1.0 shipped before the venue bridge test, at the owner's call.

1. **Bridge at the venue** once the owner has the **WiTalk9 Link cable kit**: sound card link with the WiTalk9 (master or remote headset's TRRS jack, hold ENC 3 s until the mic light is solid purple, sidetone off), NDI outputs against a real NDI runtime, vMix tally. Setup: `docs/BRIDGE.md`. Fixes go out as `v1.0.x`.
2. Beltpack bring-up (see Hardware status). Firmware version strings still say `0.1.0`.

## Owner's setup

- Server runs **off-site in OrbStack on a Mac** (`deploy/docker-compose.yml`, Cloudflare tunnel → `http://openline:8999`). Updates: replace the code keeping `deploy/data`, then `docker compose up -d --build`. Cloudflare's Browser Cache TTL is set to "Respect Existing Headers" (otherwise phones keep old JS/CSS for 4 h).
- The crew mostly use iPhones; background audio and the home-screen app are the recommended setup.
- The bridge runs on a **Windows PC at the venue** (NDI program/announcer feeds, vMix tally, and now sound card links).
- Phone admin: `/m.html`.
- The beltpack build guide is a claude.ai artifact: https://claude.ai/artifact/Hu4TmQ7SRq2mHFrXhsCX6i (wiring, parts list, case section). Read it before updating it.
