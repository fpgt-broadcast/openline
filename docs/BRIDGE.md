# The bridge (venue PC)

`openline-bridge` runs on a computer **at the venue**. It connects out to the server (no port forwarding) and links the intercom to what's at the venue:

| Want | Section |
|---|---|
| Program audio to everyone, director ↔ announcers over NDI | [NDI links](#ndi-links-program-and-announcers) |
| ON AIR / PREVIEW from vMix on people's phones and beltpacks | [vMix tally](#vmix-tally) |
| A channel out at the venue as an NDI source or on a sound card | [Channel outputs](#channel-outputs) |
| Another headset system (Saramonic WiTalk9), walkie-talkies, a mixer | [Sound card links](#sound-card-links-witalk9-and-others) |

## Setup

1. **Windows:** unzip the release's bridge zip; it has `opus.dll` and `portaudio.dll` and its own `README.txt`. **macOS:** `brew install opus portaudio`. **Linux:** `sudo apt install libopus0 libportaudio2`.
2. For NDI, install **NDI Tools** from ndi.video (it includes the NDI runtime).
3. Make a config, then fill in the server URL and the parts you need from the sections below:

   ```bash
   openline-bridge -init > bridge.json
   openline-bridge -list        # NDI sources it can see
   openline-bridge -devices     # sound card inputs and outputs
   openline-bridge -config bridge.json
   ```

Every 10 s each link logs a status line, e.g. `[Announcers] online · in peak −18 dB, sent 4.2s · out peak −12 dB`. It reconnects by itself if the server, network or a source drops. To start it with the computer: Task Scheduler (Windows, *At startup*), launchd (macOS) or systemd (Linux).

Each **link** in `bridge.json` signs in as a user with the **NDI bridge** role (Admin → Users; copy its token from **Edit**) and can take audio **in** (talks onto channels) and/or send channels **out**. A link never hears itself back, so there's no echo.

## NDI links: program and announcers

1. **Admin → Channels:** make `Program` *listen-only + program*, and have an `Announcers` channel.
2. **Admin → Users:** `Program Feed` (NDI bridge, *Talk* on Program) and `Announcer Link` (NDI bridge, *Talk + Listen* on Announcers). Give the director *Talk + Listen* on Announcers.
3. In `bridge.json`:

   ```json
   "links": [
     { "name": "Program", "token": "<Program Feed token>", "talk": ["Program"],
       "in": { "type": "ndi", "source": "PRODUCTION-PC (Program)", "music": true } },
     { "name": "Announcers", "token": "<Announcer Link token>", "talk": ["Announcers"], "listen": ["Announcers"],
       "in":  { "type": "ndi", "source": "BOOTH-PC (Talkback)", "channels": [0], "gateDb": -45 },
       "out": { "type": "ndi", "name": "OpenLine IFB Announcers" } }
   ]
   ```

4. In the booth, feed the announcers' headphones from the NDI source **OpenLine IFB Announcers**.

Program plays to everyone and dips 12 dB while anyone talks. The director talks to the booth on *Announcers*; the booth's talkback mic is heard there whenever it's louder than the gate.

## vMix tally

1. **Admin → Users → Edit** each camera operator: enter their vMix input number(s) under *vMix tally inputs*.
2. In `bridge.json` (any NDI-bridge user's token works):

   ```json
   "vmix": { "host": "192.168.1.20", "port": 8099, "token": "<Program Feed token>" }
   ```

The bridge PC must reach the vMix PC on TCP 8099. A bridge can run with only `vmix` (then no NDI or libopus is needed). Tally clears if vMix is unreachable for 5 s, so nobody keeps a stale ON AIR.

**Full-screen tally view** (a phone on a camera): the screen turns red or green, with a big talk button. Turn it on in the app (Menu), live for one person (Admin → Live), or as a user's default (Admin → Users → *tally screen*).

## Channel outputs

Play a channel at the venue as its own NDI source and/or on a sound card output (USB interface, Dante Virtual Soundcard…).

1. **Admin → Users:** add a user (e.g. `Venue Outputs`) and tick **channel outputs**. Copy its token.
2. **Admin → Channels:** in the **Output** column tick **NDI** and/or pick a **Card out** number. One outputs login covers every show.
3. In `bridge.json` (leave out `card` for NDI only):

   ```json
   "outputs": { "token": "<Venue Outputs token>", "ndiPrefix": "OpenLine",
                "card": { "device": "Dante Virtual Soundcard", "latencyMs": 60 } }
   ```

NDI outputs appear as `OpenLine <channel>` (`OpenLine <show> <channel>` with several shows). Two channels on the same card output are mixed. Changes in admin apply live.

## Sound card links: WiTalk9 and others

A link's `in` and `out` can be a sound card, so another system's crew sounds like one more person on an OpenLine channel and hears that channel.

**Saramonic WiTalk9** needs the **WiTalk9 Link cable kit** (TRRS cable + USB sound card). A plain aux cable only carries audio one way.

1. **Admin → Users:** add `WiTalk` with the **NDI bridge** role and *Talk + Listen* on the crew's channel (e.g. Camera). Copy its token.
2. Plug the USB sound card into the bridge PC and connect it to the WiTalk **master or remote** headset with the Link cable.
3. On that headset, **hold ENC for 3 s** until the mic light is **solid purple** (off after every power-up). Keep its sidetone off.
4. In `bridge.json` (`USB Audio` = part of the name shown by `-devices`; on Windows a shared part matches both the input and the output):

   ```json
   { "name": "WiTalk", "token": "<WiTalk token>", "talk": ["Camera"], "listen": ["Camera"],
     "in":  { "type": "card", "device": "USB Audio", "gateDb": -45 },
     "out": { "type": "card", "device": "USB Audio" } }
   ```

`gateDb` makes the link talk only while someone speaks: raise it (e.g. `-40`) if noise keeps it open, lower it if quiet speech is cut. If Windows makes the USB card the default playback device, set your normal speakers back as default.

## Config reference

| Field | Meaning |
|---|---|
| `server` | Server URL |
| `ndiExtraIPs` | Comma-separated IPs for NDI discovery across subnets |
| `links[].token` | Token of a user with the NDI bridge role |
| `links[].talk` / `listen` | Channel names or ids (`"Show: Channel"` if names repeat across shows) |
| `in.type` / `out.type` | `ndi`, `card` (sound card), `tone` (test beep, in only) or `null` (out only, testing) |
| `in.source` | NDI source name, exact or a unique part |
| `in.device` / `out.device` | Sound card name or part of it, from `-devices` (`""` = default) |
| `in.channels` | Channels to mix to mono, from 0. NDI: empty = all. Card: empty = `[0]` |
| `out.channels` | Card outputs to play on, from 0. Empty = both sides of a stereo card |
| `in.gainDb` / `out.gainDb` | Trim |
| `in.gateDb`, `in.holdMs` | Only talk while louder than this; hold time (default 600 ms). Leave out for program feeds |
| `in.music` | Encode for music (program) instead of speech |
| `out.name` | Name of the NDI source the bridge publishes |
| `vmix.host`, `vmix.port`, `vmix.token` | vMix PC, TCP API port (8099), an NDI-bridge user's token |
| `outputs.token`, `outputs.ndiPrefix`, `outputs.gainDb` | Channel outputs login, NDI name prefix (`OpenLine`), trim |
| `outputs.card.device`, `outputs.card.latencyMs` | Sound card for channel outputs, its buffer (default 60) |

*NDI® is a registered trademark of Vizrt NDI AB. The bridge uses the NDI runtime installed on the machine; it doesn't include the NDI SDK.*
