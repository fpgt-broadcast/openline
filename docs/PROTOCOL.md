# OpenLine protocol (v1)

There is one WebSocket per client session, at `wss://<server>/ws`. Text frames carry JSON control messages, and binary frames carry Opus audio.

## Audio

- Opus, mono, **20 ms** per packet, any encoder rate. Browsers and the bridge encode at 48 kHz; beltpacks encode at 16 kHz.
- Client → server: `[0x01][seq u16 BE][opus]`. Send only while talking on at least one channel.
- Server → client: `[0x02][src session u16][seq u16][mask u32 BE][opus]`.
  - `mask` bit *i* means this audio is heard on the listener's channel at index *i* of the `channels` array in its `welcome`.
  - Each talker arrives as a separate stream. **The client mixes.** The server never mixes or transcodes, and never sends a session its own audio (mix-minus).

## Control

**Client → server**

| Message | Notes |
|---|---|
| `{"t":"hello","token":"…"}` or `{"t":"hello","user":"Name","pin":"1234"}` | Must be the first message. Add `"client":"web\|beltpack\|bridge"`, `"ver":1`. Beltpacks send `"device":"<mac hex>"`. A beltpack with no token is accepted as *unassigned* only while the admin's new-beltpack window is open; a token naming a device must be that device's assigned user. |
| `{"t":"talk","ch":1,"on":true}` | Also `listen` and `call` (call = flash/alert on that channel) |
| `{"t":"ping","ts":123.4}` | The server answers `pong` with the same `ts` |
| `{"t":"telemetry","bat":87,"chg":false,"rssi":-60,"net":"wifi","ip":"…","fw":"…","up":3600,"hs":true,"poe":false,"disp":"oled"}` | Beltpacks, every ~5 s. `bat:-1` = external power. |

**Server → client**

| Message | Notes |
|---|---|
| `welcome` | `{sid, server, user, admin, channels:[{id,name,color,canTalk,canListen,listenOnly,program,show,out}], talk:[ids], listen:[ids], muted}`. Re-sent whenever the admin changes the config. `show` is the channel's show name (only when the server has more than one show). `out` (`{ndi, card}`) is sent only to a "channel outputs" bridge login: where the bridge should play that channel. |
| `self` | `{talk:[ids], listen:[ids], muted}`. The server-authoritative state of this session. |
| `state` | `{channels:[{id, talkers:[{sid,name}], calls:[…], members:[…]}]}`. Activity on your channels. |
| `remote` | Admin action: `{"op":"mute","on":true}`, `{"op":"vol","ch":1,"v":80}`, `{"op":"identify"}`, `{"op":"reboot"}` |
| `assign` | Beltpack: `{"token","user"}`. Store it and reconnect as that user. |
| `netcfg` | Beltpack: `{mode, dhcp, ip, mask, gw, dns, ssid, pass, server}`. Store it and reboot. |
| `settings` | Beltpack: `{name, micType, micGain, sidetone, hpLevel, bright}`. Sent on connect and on change. |
| `error` | `{"msg":"…"}`. A failed login closes with code 4001; "not now, try again later" (too many wrong guesses, new-beltpack window closed) with 4002. |

## Admin REST API

Send `Authorization: Bearer <token>` (header only) from `POST /api/admin/login {"password"}` or `{"user","pin"}` (an admin user with a PIN of 6+ digits). Tokens last 12 h.

| Method & path | Body |
|---|---|
| `GET/PUT /api/config` | Whole config (channels, users). Optional `newPassword`. |
| `GET /api/sessions` | Live sessions, including telemetry |
| `POST /api/sessions/{sid}/remote` | `{"op":"talk\|listen\|call","ch":1,"on":true}`, `{"op":"mute","on":true}`, `{"op":"vol","ch":1,"v":80}`, `{"op":"identify\|reboot\|kick"}` |
| `GET /api/devices` | Known beltpacks + live telemetry; `pending: true` = connected, not adopted yet |
| `PUT /api/devices/{id}` | `{"name","userId","audio":{micType,micGain,sidetone,hpLevel,bright}}` |
| `PUT /api/devices/{id}/net` | `{"mode":"wifi\|ethernet\|ble","dhcp":false,"ip","mask","gateway","dns","ssid","pass","server"}` |
| `DELETE /api/devices/{id}` | Forget a beltpack (also drops a waiting, unassigned one) |
| `GET/POST /api/pairing` | New-beltpack window: `{"minutes":10}` opens it (max 60), `{"minutes":0}` closes it. Returns `{open, until, secondsLeft}` |
| `GET /api/qr?text=…` | PNG QR code |

## Beltpack Bluetooth GATT

Service `4f4c0001-8e2b-4d6e-9c1a-5b3f2a7d1e60`. Characteristics are `4f4c00NN-…`:

| NN | Dir | Content |
|---|---|---|
| 02 | app → device, write | JSON: `{"t":"key","k":0,"n":"PROD","c":"e5484d","ct":1,"cl":1,"tk":0,"ls":1,"ac":0,"ca":0,"v":75}`, `{"t":"st","u":"Director","on":1,"m":0}`, `{"t":"mode","bridge":1,"audio":1}` (`audio:0` = beltpack is a remote; audio on the phone) |
| 03 | device → app, notify | `{"t":"talk\|listen\|call","k":0,"on":1}`, `{"t":"vol","k":0,"v":80}`, `{"t":"hello","keys":4}` |
| 04 | device → app, notify | `[seq u16][opus]`: mic audio while talking (only when `audio:1`) |
| 05 | app → device, write w/o response | `[seq u16][opus]`: the app's mix for the headset |
| 06 | write | Provisioning: `{"k":"ssid","v":"…"}` … then `{"commit":1}` (saves and reboots). Keys: `mode, ssid, pass, server, token, dhcp, ip, mask, gw, dns, name` |
| 07 | read | `{"id","fw","mode","name"}` |
