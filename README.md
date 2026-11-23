# OpenLine: open-source IP intercom

A self-hosted party-line intercom: an open alternative to Unity Intercom, plus a low-cost hardware beltpack in the spirit of Green-GO.

```
 phones / browsers ──┐                         ┌── admin page (desktop or phone)
 beltpacks ──────────┤ ── wss ── OpenLine ─────┤
 bridge PC at venue ─┘          server         └── invite links / QR codes
 (NDI, sound cards, vMix)    (off-site, HTTPS)
```

## What it does

- **App** in any modern browser (phone, tablet, computer). Pick channels, hold the big button to talk or tap to latch. Keeps working with the phone locked or in another app. Keyboard, foot pedal and headphone-button talk.
- **Server**: one binary. It forwards audio without mixing (about 100–300 ms mouth to ear), serves the app and an admin page, and runs fine behind a Cloudflare Tunnel.
- **Admin** (`/admin.html`, or `/m.html` on a phone): channels, users, invite links, live view with remote mute/talk/listen, beltpacks.
- **Shows**: run two shows at once, each with its own channels and users.
- **vMix tally**: ON AIR / PREVIEW per person, with an optional full-screen tally view.
- **Bridge** (a PC at the venue): NDI program feed and announcer talkback, channels out as NDI or on a sound card, other headset systems (e.g. Saramonic WiTalk9) through a sound card.
- **Beltpack** (experimental): ESP32-S3 with an XLR-4 headset jack, 4 lit keys, a knob and a screen. Bluetooth to a phone, or standalone on Wi‑Fi or Ethernet/PoE.

## Try it

```bash
make build
./bin/openline-server     # prints the admin password, starter users + PINs, and URLs
```

Open `http://localhost:8999`, sign in as `Director` with the PIN it printed, then open it in a second browser as `Camera 1`. Select *Camera* and hold the big button to talk.

## Docs

| Doc | For |
|---|---|
| [docs/DEPLOY.md](docs/DEPLOY.md) | Running the server on the internet, first setup, security, phones |
| [docs/BRIDGE.md](docs/BRIDGE.md) | The venue PC: NDI, vMix tally, channel outputs, WiTalk9 and other sound card links |
| [docs/PROTOCOL.md](docs/PROTOCOL.md) | WebSocket protocol, admin API, beltpack Bluetooth |
| [hardware/README.md](hardware/README.md) · [hardware/case](hardware/case/README.md) · [firmware](firmware/README.md) | Beltpack design, case, firmware |

## Releases

Builds are on the [Releases page](https://github.com/fpgt-broadcast/openline/releases): the server for every OS, the Windows bridge zip and the beltpack firmware. To make one: **Releases → Draft a new release**, type a new tag (e.g. `v1.0.0`, or `v1.1.0-beta1` for a pre-release), publish. The `release` workflow builds and attaches everything.

Development: `make test` runs all Go tests, `make release` cross-compiles every binary (no cgo).

## Status

| Part | State |
|---|---|
| Server, app, admin | Ready. Tested with Go tests, browsers and on an iPhone. |
| Bridge | Audio path tested with test tones and virtual sound cards. Real NDI sources, sound cards and vMix still to be tried at a venue. |
| Beltpack | **Experimental.** Firmware compiles but hasn't run on hardware; PCB not laid out yet. |

Known limits:
- A **locked iPhone mutes the mic** of a web app: unlock to talk. Listening works locked, and talking works from another app.
- iPhone Safari has no Web Bluetooth: pairing a beltpack over Bluetooth from an iPhone needs the Bluefy browser.

## License

MIT (see `LICENSE`). NDI® is a registered trademark of Vizrt NDI AB.
