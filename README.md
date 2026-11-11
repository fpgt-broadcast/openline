# OpenLine — open-source IP intercom

OpenLine is a self-hosted party-line intercom: an open alternative to Unity Intercom, with a low-cost hardware beltpack in the spirit of Green-GO.

- **Server**: one binary you run off-site. It routes Opus audio between everyone with near-zero added latency (no mixing or transcoding), serves the web app, and has an admin page for channels, users and beltpacks. Automatic HTTPS, Cloudflare Tunnel or Tailscale give an easy way to reach it from anywhere.
- **App**: works in any modern browser on phones, tablets and computers. You pick the channels to talk to and hold one big PTT button, or tap it to latch. Tap a channel for its level and to see who's talking and listening. Keyboard, foot-pedal and headphone-button talk are supported.
- **Beltpack**: ESP32-S3 + WM8960 with a standard **XLR-4 intercom headset** jack, 4 talk keys with RGB LEDs, an encoder and an OLED screen (e-ink optional). It works in several ways:
  - **Bluetooth to a phone/computer**, using the beltpack headset, or
  - **Bluetooth as a remote** (keys + screen) while audio goes through the phone's AirPods or any Bluetooth/wired headphones, or
  - **standalone over Wi‑Fi**, or
  - **standalone over Ethernet with PoE**.
- **Admin control of beltpacks**: battery, charging, signal, IP, headset detect and firmware. From the admin page you can force talk or listen per key, set volume, mute the mic remotely, identify (flash), reboot, re-assign the beltpack to another user, and push Wi‑Fi or Ethernet settings with DHCP or a static IP.
- **NDI bridge**: an on-site computer brings **program audio** in (dipped automatically while people talk) and connects the **director ↔ announcers**: their NDI talkback comes in, and an NDI IFB feed goes back out to them. Typical latency is 150–300 ms.

```
   phones / browsers ─┐                        ┌─ beltpack (Wi‑Fi / Ethernet+PoE)
   beltpack ── BLE ── phone ─┤  wss://  OpenLine server  ├─ admin page
   NDI ⇄ openline-bridge (venue) ┘     (off-site)         └─ …
```

## Quick start

```bash
make build                      # or download a release binary
./bin/openline-server           # prints admin password, URLs and a QR code
```

Open `http://localhost:8999` and sign in as `Director` / `1111`. Open the URL in a second browser as `Camera 1` / `2222`, then select *Camera* and hold the big button to talk.

For real off-site use (HTTPS is required for microphones), see **[docs/DEPLOY.md](docs/DEPLOY.md)**.

| Doc | What |
|---|---|
| [docs/DEPLOY.md](docs/DEPLOY.md) | Hosting off-site (Cloudflare Tunnel / Let's Encrypt / Tailscale), first run, connecting clients, latency |
| [docs/BRIDGE.md](docs/BRIDGE.md) | NDI program feed and director ↔ announcer setup |
| [docs/PROTOCOL.md](docs/PROTOCOL.md) | Wire protocol, admin API, beltpack BLE GATT |
| [hardware/README.md](hardware/README.md) | Beltpack reference design: XLR wiring, pin map, PoE, BOM (~$28 / ~$42 with PoE), dev-board prototype |
| [firmware/README.md](firmware/README.md) | Building and flashing the beltpack firmware |

## Repository layout

```
cmd/openline-server   server entry point (TLS / autocert / banner)
cmd/openline-bridge   NDI bridge entry point
internal/server       routing hub, config store, admin API, beltpack control
internal/bridge       NDI <-> channel links, jitter-buffered mixer, gate
internal/ndi          NDI runtime binding (loaded at run time, no SDK needed to build)
internal/opus         libopus binding (loaded at run time)
internal/client       Go protocol client (bridge + tests)
web/static            browser app + admin page (embedded into the server binary)
firmware/             ESP-IDF project for the beltpack
hardware/             beltpack reference design
deploy/               docker compose (with Cloudflare Tunnel), systemd unit
```

## Development

```bash
make test       # Go unit + integration tests (routing, permissions, admin, beltpack adopt, bridge)
make release    # cross-compiled binaries for linux/macOS/windows, amd64/arm64 (no cgo)
```

## Status

| Part | State |
|---|---|
| Server, web app, admin | Working; covered by Go tests and a two-browser end-to-end audio test |
| NDI bridge | Audio path tested with libopus and test tones. The NDI binding follows the SDK headers but needs a first run against a real NDI source. |
| Beltpack firmware | Compiles cleanly with ESP-IDF 5.3.2 (OLED/Ethernet and e-ink variants). **Not yet run on hardware.** |
| Beltpack hardware | Reference design, pin map and BOM. The PCB is not laid out yet. Start with the dev-board prototype. |

Known limits:
- iPhone Safari has no Web Bluetooth, so pairing a beltpack over Bluetooth from an iPhone needs the Bluefy browser (or a future native wrapper). Standalone Wi‑Fi/Ethernet beltpacks work with any phone.
- Audio travels over TCP (WebSocket), which works through any firewall or proxy. A UDP path for beltpacks is a possible later improvement.

## License

MIT (see `LICENSE`). NDI® is a registered trademark of Vizrt NDI AB.
