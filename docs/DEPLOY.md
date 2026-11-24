# Running the server

The server is one binary (or one Docker container) that also serves the app. Run it on any always-on computer. Each person needs about 35 kbit/s up while talking and 35 kbit/s down per person they hear.

Browsers only allow the microphone over **HTTPS**, so pick one of these:

| Option | When | Start it with |
|---|---|---|
| **1. Cloudflare Tunnel** (recommended) | Behind a router you don't want to open | `deploy/docker-compose.yml`, below |
| 2. Let's Encrypt | The server has a public IP; ports 80 + 443 forwarded | `openline-server -domain intercom.example.com` (systemd unit: `deploy/openline.service`) |
| 3. Tailscale | Private, nothing on the internet | `tailscale serve --bg 8999` (beltpacks can't join a tailnet) |
| 4. Your own reverse proxy | Caddy, nginx, Traefik already running | `openline-server -listen 127.0.0.1:8999`, proxy `/` including the `/ws` WebSocket |
| 5. Local network only | No internet at all | `openline-server -self-signed`, accept the certificate warning once per device |

Plain `http://192.168.x.x` does **not** work for talking (only `localhost` is exempt); the app says so on the login screen.

## Cloudflare Tunnel

```bash
cd deploy
echo "TUNNEL_TOKEN=<token from Cloudflare Zero Trust > Networks > Tunnels>" > .env
docker compose up -d
docker compose logs openline     # first run: admin password and starter PINs
```

1. In the tunnel, add a public hostname, e.g. `intercom.example.com` → `http://openline:8999`.
2. In the Cloudflare dashboard for your domain: **Caching → Configuration → Browser Cache TTL → Respect Existing Headers**. Otherwise phones keep old app files for hours after an update.

The compose file only opens port 8999 on the computer itself (`127.0.0.1`), and tells the server to trust the tunnel for visitors' real IPs.

**Updating:** replace the code (keep `deploy/data`), then `docker compose up -d --build`. The running version shows at the bottom of the app's Menu and in admin → Server. Update the tunnel agent with `docker compose pull cloudflared && docker compose up -d`.

## First setup

The first start prints the **admin password** and the **starter users with random PINs** (Director, Camera 1, A1). Open `/admin.html`:

1. **Server**: set the public URL (used in invite links) and change the admin password.
2. **Channels**: names and colours. Mark program feeds *listen-only + program*; they dip 12 dB while anyone talks.
3. **Users**: one per role (e.g. *Camera 1*) with a PIN, an optional person name, and channel access. The channel order is the beltpack key order.
4. Send each person their **invite link / QR code** (Users → Invite). It signs them in with no typing.

## Phones

- **Background audio** is on by default on phones: people keep hearing everyone with the phone locked or in another app, and the lock screen shows who's talking. Talking works from another app, but **a locked iPhone mutes the mic**, so unlock to talk.
- **Add to Home Screen** (Share menu) gives a full-screen OpenLine app with its own icon. It keeps its own sign-in, separate from Safari: sign in once inside it.
- Hands-free talk: a USB foot pedal that sends Space, the keyboard (Space, 1–9, C), or the headphone play/pause button (Menu → Headphone button).
- On poor cellular data, set **Menu → Network buffer → Safe**.

## Security

The server is meant to sit on the internet. What protects it:

- **PINs**: give everyone their own; 6+ digits for anything important. Admin sign-in with a PIN needs 6+ digits, otherwise use the admin password. The Users tab warns about easy PINs.
- **Wrong guesses**: 8 from one IP (for IPv6, one /64 block), or 20 for one name or the admin password, block more tries for a minute.
- **New beltpacks** can only connect while **Admin → Beltpacks → Accept new beltpacks** is on (10 minutes). Beltpacks set up over Bluetooth with a user connect any time.
- **Behind a proxy on another machine**: start the server with `-trusted-proxy <IP or host name>` so the login limits see real visitor IPs.

## Latency

About **100–300 ms** mouth to ear over the internet: 20 ms audio frames, the network both ways, and an adaptive jitter buffer (40–120 ms) at the listener. The server only forwards (< 1 ms). NDI adds 20–40 ms at the bridge.

## Backups

Everything lives in `data/config.json`. Back up that file. Forgot the admin password:

```bash
openline-server -data ./data -reset-admin 'new-password'
```
