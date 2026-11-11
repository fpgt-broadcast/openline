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

## Config reference

| Field | Meaning |
|---|---|
| `server` | Server URL (`https://…`, or `http://…` on a LAN) |
| `ndiExtraIPs` | Comma-separated IPs for NDI discovery across subnets |
| `links[].token` | Token of a user with the NDI bridge role |
| `links[].talk` / `listen` | Channel names (or ids) |
| `in.type` | `ndi`, or `tone` (test beep) |
| `in.source` | NDI source name. Matched exactly, or by a unique substring. |
| `in.channels` | Which NDI audio channels to mix to mono (0-based); empty = all |
| `in.gainDb` | Input trim |
| `in.gateDb` | Only talk while louder than this (e.g. `-45`). Omit for always-on feeds such as program. |
| `in.holdMs` | Gate hold time (default 600) |
| `in.music` | Encode for music (program) instead of speech |
| `out.type` | `ndi`, or `null` (testing) |
| `out.name` | Name of the NDI source the bridge publishes |
| `out.gainDb` | Output trim |

## Latency

NDI receive (one audio block, 20–40 ms) + Opus encode (20 ms) + network + listener jitter buffer (≈60 ms). Program and announcer audio typically arrive in **150–300 ms**, well inside the 1 s target. On the way out, the bridge's own 60 ms jitter buffer adds to the NDI output.

## Running as a service

- Windows: use NSSM or Task Scheduler (*At startup*, run whether logged on or not).
- macOS: a launchd plist.
- Linux: a systemd unit like `deploy/openline.service`.

The bridge reconnects automatically if the server, the network or an NDI source drops.

*NDI® is a registered trademark of Vizrt NDI AB. The bridge loads the NDI runtime installed on the machine; it does not ship the NDI SDK.*
