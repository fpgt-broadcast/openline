# NDI bridge: program audio and announcer talkback

`openline-bridge` runs on a computer **at the venue**, on the same network as your NDI sources. It connects out to the OpenLine server, so it needs no port forwarding. Each entry in its config is a **link**. A link is one intercom session that can:

- **take an NDI source in** and talk it onto channels (program feed, announcer talkback mic), and/or
- **send channels out as a new NDI source** (e.g. an IFB/cue feed back to the announcers).

When a link has both, the announcers never hear their own voice echoed back. The server never sends a session its own audio (mix-minus).

## Typical setup: director ↔ announcers + program

```
 Announce booth                         On-site bridge PC                 Off-site server        Crew
 ┌───────────────┐  NDI "Talkback"  ┌───────────────────────┐   wss    ┌──────────────┐    ┌─────────┐
 │ talkback mic  │ ───────────────► │ link "Announcers"     │ ───────► │  channel     │ ◄─►│Director │
 │ headphones ◄──┼── NDI "OpenLine  │   in  → talk          │ ◄─────── │ "Announcers" │    │app/belt │
 └───────────────┘   IFB Announcers"│   out ← listen        │          │              │    └─────────┘
 Production PC  NDI "Program" ────► │ link "Program" in→talk│ ───────► │ "Program"    │ ──► everyone
                                    └───────────────────────┘          └──────────────┘    (ducked)
```

1. **Admin → Channels.** Make sure `Announcers` exists (it's in the default config). Make `Program` *listen-only + program*.
2. **Admin → Users**:
   - `Announcer Link`: role **NDI bridge**, *Talk + Listen* on `Announcers`.
   - `Program Feed`: role **NDI bridge**, *Talk* on `Program`.
   - Give the Director (and anyone else who should hear or reach the booth) *Talk + Listen* on `Announcers` and *Listen* on `Program`.
   - Copy each bridge user's **token** from its Edit panel.
3. On the bridge PC, install **NDI Tools** (which includes the NDI runtime) from ndi.video and **libopus**:
   - Windows: put `opus.dll` next to `openline-bridge.exe` (from any libopus build, e.g. the msys2 `mingw-w64-x86_64-opus` package), or set `OPUS_LIB`.
   - macOS: `brew install opus`
   - Linux: `sudo apt install libopus0`
4. List the NDI sources it can see:

   ```bash
   openline-bridge -list
   ```

5. Create the config, then edit the server URL, tokens and NDI source names:

   ```bash
   openline-bridge -init > bridge.json
   ```

   ```json
   {
     "server": "https://intercom.example.com",
     "links": [
       {
         "name": "Program",
         "token": "<Program Feed token>",
         "talk": ["Program"],
         "in": { "type": "ndi", "source": "PRODUCTION-PC (Program)", "channels": [0, 1], "music": true }
       },
       {
         "name": "Announcers",
         "token": "<Announcer Link token>",
         "talk": ["Announcers"],
         "listen": ["Announcers"],
         "in":  { "type": "ndi", "source": "BOOTH-PC (Talkback)", "channels": [0], "gateDb": -45 },
         "out": { "type": "ndi", "name": "OpenLine IFB Announcers" }
       }
     ]
   }
   ```

6. Run the bridge:

   ```bash
   openline-bridge -config bridge.json
   ```

   Every 10 s each link logs `online · in peak −18 dB, sent 4.2s · out peak −12 dB`.
7. In the booth's NDI-capable mixer or NDI Tools, subscribe the announcers' headphone feed to **OpenLine IFB Announcers**.

**Result:** the director selects *Announcers* and holds PTT, and the announcers hear them in their IFB. When an announcer presses their talkback (or just speaks into the talkback mic, past the gate), the director hears them on the *Announcers* channel. Program plays to everyone and dips 12 dB whenever someone talks.

## vMix tally

The bridge can read tally from vMix and show each person whether their camera is **ON AIR** or in **PREVIEW**. The app shows a red ON AIR bar and frame (green for preview), the beltpack shows `LIVE`/`PVW` with a frame on the screen, and the admin Live tab shows it too.

1. **Admin → Users → Edit** a camera operator. Under *vMix tally inputs*, enter their vMix input number(s), e.g. `2`.
2. Add to `bridge.json` (any user with the NDI bridge role can send tally):

   ```json
   "vmix": { "host": "192.168.1.20", "port": 8099, "token": "<Program Feed token>" }
   ```

   The bridge PC must be able to reach the vMix PC on TCP **8099** (vMix's TCP API, always on). A bridge can run with only a `vmix` section and no NDI links; then neither NDI nor libopus is needed.

**Full-screen tally view:** for a phone mounted on a camera, the whole screen turns red (ON AIR) or green (PREVIEW), with a big talk button kept at the bottom (tap = latch, hold = talk to the person's selected channels). Turn it on from the app (**Menu → Full-screen tally view**), for one person live (**Admin → Live → Tally screen**), or as a default for a user (**Admin → Users → tally screen**). **Exit** returns to the normal view.

The bridge subscribes with `SUBSCRIBE TALLY`, so changes arrive instantly. If vMix becomes unreachable for more than 5 s, tally is cleared so nobody keeps a stale ON AIR light.

## Channel outputs: NDI and sound card

The bridge can play any intercom channel out at the venue: as **its own NDI source** (for vMix, a recorder, an NDI mixer), on a **sound card output** (a USB interface, Dante Virtual Soundcard, BlackHole…), or both. You choose it per channel in the admin page; the bridge picks up changes live.

1. **Admin → Users**: add a user (e.g. `Venue Outputs`) and tick **channel outputs**. Save, then copy its token from **Edit**.
2. **Admin → Channels**: in the **Output** column, tick **NDI** and/or choose a **Card out** number for each channel you want played out. With several shows, pick each show at the top of the page; one outputs login covers every show.
3. For sound card outputs, install PortAudio on the bridge PC. The Windows zip already includes `portaudio.dll`. On macOS run `brew install portaudio`, on Linux `sudo apt install libportaudio2`.
4. List the sound cards and pick one:

   ```bash
   openline-bridge -devices
   ```

5. Add an `outputs` section to `bridge.json`:

   ```json
   "outputs": {
     "token": "<Venue Outputs token>",
     "ndiPrefix": "OpenLine",
     "card": { "device": "Dante Virtual Soundcard", "latencyMs": 60 }
   }
   ```

   `device` is the name from `-devices`, or part of it (`""` = the default output). Leave out `card` if you only use NDI outputs.

**Result:** every channel with NDI ticked shows up as an NDI source named `OpenLine <channel>`, or `OpenLine <show> <channel>` when there are several shows. Every channel with a card output plays on that output number of the sound card. Two channels set to the same card output are mixed together. The bridge logs each output's level every 10 s.

## Sound card links: WiTalk9 and other headset systems

A link's `in` and `out` can be a **sound card** instead of NDI. That connects OpenLine to anything with an audio jack: a wireless headset system like the Saramonic WiTalk9, a wired party-line interface, walkie-talkies or a mixer. The other system's crew then sounds like one more person on an OpenLine channel, and they hear that channel.

The bridge runs on a computer **at the venue** (near the other system), not on the OpenLine server. It only needs an internet connection out to the server.

### Saramonic WiTalk9

You need the **WiTalk9 Link cable kit** (sold separately): its TRRS cable plus the TRRS-to-USB sound card. A plain 3-pole aux cable carries audio one way only. A generic 4-pole cable may not work either, because the same Saramonic cable is used to cascade two WiTalk systems.

1. **Admin → Users:** add a user, e.g. `WiTalk`, with the **NDI bridge** role and **Talk + Listen** on the channel the WiTalk crew should be on (e.g. Camera). Copy its token.
2. Plug the USB sound card into the bridge PC. Connect it to the WiTalk **master or remote** headset with the Link cable.
3. On that headset, **hold ENC for 3 seconds** until the mic light is **solid purple** (cascade/link connection on). This is off after every power-up. Keep the headset's **sidetone off** while linked.
4. Find the sound card's name:

   ```bash
   openline-bridge -devices
   ```

   On Windows the input and output have different names, e.g. `Microphone (USB Audio Device)` and `Speakers (USB Audio Device)`. A shared part such as `USB Audio` matches both.
5. Add a link to `bridge.json`:

   ```json
   {
     "name": "WiTalk",
     "token": "<WiTalk user token>",
     "talk": ["Camera"],
     "listen": ["Camera"],
     "in":  { "type": "card", "device": "USB Audio", "gateDb": -45 },
     "out": { "type": "card", "device": "USB Audio" }
   }
   ```

**Result:** whatever the WiTalk crew says is heard on Camera, and they hear everyone talking on Camera. The server never sends a link its own audio, so they don't hear themselves echoed. `gateDb` makes the link "talk" only while someone on the WiTalk is speaking, so OpenLine shows them as talking only then. Raise it (e.g. `-40`) if background noise keeps it open; lower it if quiet speech gets cut.

If Windows makes the USB sound card its default playback device, set your normal speakers back as the default so system sounds don't go to the crew.

### Any other sound card

- `in.device` / `out.device`: the name (or part of it) from `openline-bridge -devices`; `""` = the default device.
- `in.channels`: which inputs to use, counting from 0, mixed to mono. Default `[0]`.
- `out.channels`: which outputs to play on, counting from 0. Default: both sides of a stereo card.
- The card is used at 48 kHz when it supports it. Inputs fall back to the card's own rate.

## Config reference

| Field | Meaning |
|---|---|
| `server` | Server URL (`https://…`, or `http://…` on a LAN) |
| `ndiExtraIPs` | Comma-separated IPs for NDI discovery across subnets |
| `links[].token` | Token of a user with the NDI bridge role |
| `links[].talk` / `listen` | Channel names (or ids) |
| `in.type` | `ndi`, `card` (sound card), or `tone` (test beep) |
| `in.device` | `card`: sound card input name, or part of it (`""` = default input) |
| `in.source` | NDI source name. Matched exactly, or by a unique substring. |
| `in.channels` | Which audio channels to mix to mono (0-based). NDI: empty = all. Card: empty = `[0]` |
| `in.gainDb` | Input trim |
| `in.gateDb` | Only talk while louder than this (e.g. `-45`). Omit for always-on feeds such as program. |
| `in.holdMs` | Gate hold time (default 600) |
| `in.music` | Encode for music (program) instead of speech |
| `out.type` | `ndi`, `card` (sound card), or `null` (testing) |
| `out.device` | `card`: sound card output name, or part of it (`""` = default output) |
| `out.channels` | `card`: outputs to play on (0-based); empty = both sides of a stereo card |
| `out.name` | Name of the NDI source the bridge publishes |
| `out.gainDb` | Output trim |
| `vmix.host` / `vmix.port` | vMix PC and TCP API port (default 8099) |
| `vmix.token` | Token of an NDI-bridge user, used to publish tally |
| `outputs.token` | Token of the user with the **channel outputs** role |
| `outputs.ndiPrefix` | Start of each output's NDI source name (default `OpenLine`) |
| `outputs.gainDb` | Trim for all channel outputs |
| `outputs.card.device` | Sound card name (or part of it) from `openline-bridge -devices`; `""` = default output |
| `outputs.card.latencyMs` | Sound card buffer (default 60) |

With several shows, a link's `talk` / `listen` can name a channel as `"Show name: Channel"` when the bridge user has channels with the same name in different shows.

## Latency

NDI receive (one audio block, 20–40 ms) + Opus encode (20 ms) + network + listener jitter buffer (≈60 ms). Program and announcer audio typically arrive in **150–300 ms**, well inside the 1 s target. On the way out, the bridge's own 60 ms jitter buffer adds to the NDI output.

## Running as a service

- Windows: use NSSM or Task Scheduler (*At startup*, run whether logged on or not).
- macOS: a launchd plist.
- Linux: a systemd unit like `deploy/openline.service`.

The bridge reconnects automatically if the server, the network or an NDI source drops.

*NDI® is a registered trademark of Vizrt NDI AB. The bridge loads the NDI runtime installed on the machine; it does not ship the NDI SDK.*
