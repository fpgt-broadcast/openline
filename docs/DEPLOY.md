# Running the server off-site

The server is one binary with no dependencies, and it serves the web app itself. Run it on any always-on computer (a small VPS, a mini-PC at the office, a Raspberry Pi 4/5). Each client needs about 35 kbit/s up while talking, and about 35 kbit/s down per person they're hearing.

Browsers only allow microphone access over **HTTPS**, so you need one of the options below. Pick whichever matches your situation.

## Option 1: Cloudflare Tunnel (recommended, no port forwarding)

Use this when the server sits behind a router you can't or don't want to open. It's free on a domain managed by Cloudflare.

```bash
cd deploy
echo "TUNNEL_TOKEN=<token from Cloudflare Zero Trust > Networks > Tunnels>" > .env
docker compose up -d
docker compose logs openline     # shows the first-run admin password
```

In the tunnel, add a public hostname such as `intercom.example.com` → `http://openline:8999`. WebSockets work through Cloudflare by default. Then open `https://intercom.example.com/admin.html`, sign in, and set **Server → Public URL** so invite links and QR codes use it.

To update the tunnel agent (the Cloudflare dashboard flags old versions as *Unsupported*):

```bash
docker compose pull cloudflared && docker compose up -d
```

No Docker? Install `cloudflared` and run `cloudflared tunnel run --token …` next to `openline-server`.

## Option 2: Public IP + automatic Let's Encrypt

Forward TCP 80 and 443 to the server and point a DNS name at it.

```bash
./openline-server -domain intercom.example.com -data /var/lib/openline
```

Certificates are fetched and renewed automatically. See `deploy/openline.service` for a systemd unit.

## Option 3: Tailscale (private network)

Install Tailscale on the server and every client, then run `tailscale serve --bg 8999`. Everyone uses the `https://<machine>.<tailnet>.ts.net` URL. Nothing is exposed to the internet. Beltpacks can't join a tailnet, so use the BLE phone mode with them, or use Option 1 or 2.

## Option 4: Behind an existing reverse proxy

Run `openline-server -listen 127.0.0.1:8999` and proxy `/` (including the WebSocket upgrade on `/ws`) from Caddy, nginx or Traefik. Caddy example:

```
intercom.example.com {
    reverse_proxy 127.0.0.1:8999
}
```

## First run

1. Start the server. It prints the **admin password** once, plus the LAN URLs and a QR code.
2. Open `/admin.html`:
   - **Server**: set the public URL and change the admin password.
   - **Channels**: name them, set colours, and mark program feeds *listen-only + program* (they're dipped 12 dB automatically while anyone talks).
   - **Users**: one per person or role. Choose channel access (*Talk + Listen*, *Listen*…) and order (= beltpack key order). Share the **invite link / QR**. Opening it signs the person in, with no typing.
3. The demo config has Director/1111, Camera 1/2222 and A1/3333, plus the bridge users *Program Feed* and *Announcer Link*. Change or delete them.

## Connecting clients

| Client | How |
|---|---|
| Phone / tablet / computer | Open the invite link (or the URL plus name + PIN). Add it to the home screen to run it full-screen. |
| Beltpack over Bluetooth | In the app: **Menu → Connect beltpack**. Choose whether audio goes through the beltpack's headset, or through the phone (AirPods, any Bluetooth/wired headset) with the beltpack as the keys and screen. |
| Beltpack standalone (Wi‑Fi / Ethernet / PoE) | **Admin → Beltpacks → Set up beltpack via Bluetooth** (Chrome/Edge). Enter Wi‑Fi details, the server URL and the user. After that, every beltpack setting can be changed from Admin, including a static IP. |
| NDI program / announcers | Run `openline-bridge` on a computer at the venue. See [BRIDGE.md](BRIDGE.md). |
| Hands-free talk | A USB foot pedal that sends Space, the keyboard (Space / 1–9 / C), or the headphone play/pause button (Menu → *Headphone button*). |

## Latency

These figures are mouth to ear, one person to another, over the internet.

| Stage | Typical |
|---|---|
| Capture frame + encode | 20–25 ms |
| Network to server | 10–60 ms |
| Server forwarding (no mixing, no transcoding) | < 1 ms |
| Network to listener | 10–60 ms |
| Jitter buffer (adaptive, starts at 60 ms) | 40–120 ms |
| Decode + output buffer | 10–30 ms |
| **Total** | **~100–300 ms** |

NDI sources add one NDI audio block (~20–40 ms) at the bridge, which keeps program and announcer audio well under 1 s.

If a phone on poor cellular data stutters, set **Menu → Network buffer → Safe**.

## Backups

All state lives in `data/config.json` (channels, users, tokens, beltpacks). Back up that file. To reset the admin password:

```bash
openline-server -data ./data -reset-admin 'new-password'
```
