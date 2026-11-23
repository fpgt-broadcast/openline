package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"openline/internal/client"
	"openline/internal/proto"
)

type harness struct {
	t     *testing.T
	srv   *Server
	ts    *httptest.Server
	admin string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store, pass, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(store, fstest.MapFS{"index.html": {Data: []byte("hi")}})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	h := &harness{t: t, srv: srv, ts: ts}
	var res struct{ Token string }
	h.do("POST", "/api/admin/login", map[string]string{"password": pass}, &res, 200)
	h.admin = res.Token
	return h
}

func (h *harness) do(method, path string, body, out any, want int) {
	h.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, h.ts.URL+path, rd)
	if h.admin != "" {
		req.Header.Set("Authorization", "Bearer "+h.admin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		var b bytes.Buffer
		b.ReadFrom(resp.Body)
		h.t.Fatalf("%s %s: status %d want %d: %s", method, path, resp.StatusCode, want, b.String())
	}
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
}

type recorder struct {
	mu    sync.Mutex
	audio []string
	masks []uint32
	ctrl  []string
}

func (r *recorder) has(t string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.ctrl {
		if strings.Contains(c, t) {
			return true
		}
	}
	return false
}

func (r *recorder) nAudio() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.audio)
}

func (h *harness) connect(hello proto.Msg) (*client.Client, *recorder) {
	h.t.Helper()
	c, err := client.Dial(context.Background(), h.ts.URL, hello)
	if err != nil {
		h.t.Fatal(err)
	}
	r := &recorder{}
	c.OnAudio = func(src, seq uint16, mask uint32, opus []byte) {
		r.mu.Lock()
		r.audio = append(r.audio, string(opus))
		r.masks = append(r.masks, mask)
		r.mu.Unlock()
	}
	c.OnControl = func(t string, raw []byte) {
		r.mu.Lock()
		r.ctrl = append(r.ctrl, string(raw))
		r.mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.t.Cleanup(cancel)
	go c.Run(ctx)
	return c, r
}

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if f() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestLoginFails(t *testing.T) {
	h := newHarness(t)
	_, err := client.Dial(context.Background(), h.ts.URL, proto.Msg{User: "Director", PIN: "nope"})
	if err == nil {
		t.Fatal("expected login failure")
	}
}

func TestPartyLineRouting(t *testing.T) {
	h := newHarness(t)
	dir, _ := h.connect(proto.Msg{User: "Director", PIN: "1111", Client: "test"})
	cam, camRec := h.connect(proto.Msg{User: "Camera 1", PIN: "2222", Client: "test"})
	a1, a1Rec := h.connect(proto.Msg{User: "a1", PIN: "3333", Client: "test"})
	_ = a1

	// Director talks on Camera (id 2): Camera 1 hears it, A1 (no Camera access) doesn't.
	if err := dir.SetTalk(2, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "talk state", func() bool { return camRec.has(`"talkers":[{"sid":1,"name":"Director"}]`) })
	dir.SendAudio(1, []byte("hello-cam"))
	eventually(t, "camera audio", func() bool { return camRec.nAudio() == 1 })
	camRec.mu.Lock()
	// Camera 1's channel list is [2,1,5] so Camera is index 0 -> mask bit 0.
	if camRec.masks[0] != 1 || camRec.audio[0] != "hello-cam" {
		t.Fatalf("bad mask/payload %v %q", camRec.masks, camRec.audio)
	}
	camRec.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	if a1Rec.nAudio() != 0 {
		t.Fatal("A1 should not hear Camera channel")
	}

	// Talking on Production reaches both; listener masks reflect their own order.
	dir.SetTalk(1, true)
	time.Sleep(50 * time.Millisecond)
	dir.SendAudio(2, []byte("both"))
	eventually(t, "a1 audio", func() bool { return a1Rec.nAudio() == 1 })
	eventually(t, "cam audio 2", func() bool { return camRec.nAudio() == 2 })
	camRec.mu.Lock()
	if camRec.masks[1] != 0b11 { // Camera(0) + Production(1)
		t.Fatalf("cam mask = %b", camRec.masks[1])
	}
	camRec.mu.Unlock()

	// Camera 1 stops listening to Production: still hears via Camera.
	cam.SetListen(1, false)
	time.Sleep(50 * time.Millisecond)
	dir.SendAudio(3, []byte("x"))
	eventually(t, "cam audio 3", func() bool { return camRec.nAudio() == 3 })
	camRec.mu.Lock()
	if camRec.masks[2] != 0b01 {
		t.Fatalf("cam mask after unlisten = %b", camRec.masks[2])
	}
	camRec.mu.Unlock()

	// No self-echo: director never receives own audio. A1 cannot talk on Program.
	a1.SetTalk(5, true)
	eventually(t, "permission error", func() bool { return a1Rec.has("not allowed") })
}

func TestAdminMuteAndRemote(t *testing.T) {
	h := newHarness(t)
	dir, dirRec := h.connect(proto.Msg{User: "Director", PIN: "1111", Client: "test"})
	_, camRec := h.connect(proto.Msg{User: "Camera 1", PIN: "2222", Client: "test"})
	dir.SetTalk(1, true)
	time.Sleep(50 * time.Millisecond)

	var sessions []SessionInfo
	h.do("GET", "/api/sessions", nil, &sessions, 200)
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d", len(sessions))
	}
	h.do("POST", "/api/sessions/1/remote", RemoteOp{Op: "mute", On: true}, nil, 200)
	eventually(t, "muted self", func() bool { return dirRec.has(`"muted":true`) })
	dir.SendAudio(1, []byte("muted"))
	time.Sleep(80 * time.Millisecond)
	if camRec.nAudio() != 0 {
		t.Fatal("muted session audio was forwarded")
	}
	dir.SetTalk(1, true)
	eventually(t, "mute error", func() bool { return dirRec.has("muted by the admin") })

	h.do("POST", "/api/sessions/2/remote", RemoteOp{Op: "vol", Ch: 1, V: 42}, nil, 200)
	eventually(t, "vol remote", func() bool { return camRec.has(`"op":"vol","v":42`) })
	h.do("POST", "/api/sessions/99/remote", RemoteOp{Op: "identify"}, nil, 404)
}

func TestBeltpackAdoptAssignAndNet(t *testing.T) {
	h := newHarness(t)
	// A fresh beltpack with only a device id connects unassigned once the
	// admin opens the new-beltpack window.
	h.do("POST", "/api/pairing", map[string]int{"minutes": 10}, nil, 200)
	bp, bpRec := h.connect(proto.Msg{Client: "beltpack", Device: "AA:BB:CC:00:11:22"})
	eventually(t, "settings push", func() bool { return bpRec.has(`"t":"settings"`) })
	if bpRec.has(`"channels":[{`) {
		t.Fatal("unassigned device must not get channels")
	}
	var devs []DeviceInfo
	h.do("GET", "/api/devices", nil, &devs, 200)
	if len(devs) != 1 || devs[0].ID != "aabbcc001122" || !devs[0].Online || !devs[0].Pending {
		t.Fatalf("devices = %+v", devs)
	}
	if len(h.srv.Store.Get().Devices) != 0 {
		t.Fatal("an unadopted beltpack must not be saved")
	}
	cfg := h.srv.Store.Get()
	cam := cfg.Users[1]

	// Assign it to Camera 1 -> device receives that user's token.
	audio := defaultDeviceAudio()
	audio.Sidetone = 55
	h.do("PUT", "/api/devices/aabbcc001122", map[string]any{"name": "BP-7", "userId": cam.ID, "audio": audio}, nil, 200)
	eventually(t, "assign", func() bool { return bpRec.has(`"t":"assign","token":"` + cam.Token) })
	eventually(t, "sidetone", func() bool { return bpRec.has(`"sidetone":55`) })

	// Device reconnects with the token (as the firmware does after "assign").
	bp.Close()
	eventually(t, "old session gone", func() bool { return len(h.srv.Hub.Sessions()) == 0 })
	bp2, bp2Rec := h.connect(proto.Msg{Client: "beltpack", Device: "aabbcc001122", Token: cam.Token})

	// Static IP config is pushed to the signed-in beltpack; bad static config rejected.
	h.do("PUT", "/api/devices/aabbcc001122/net", NetConfig{Mode: "ethernet", DHCP: false, IP: "nope"}, nil, 400)
	h.do("PUT", "/api/devices/aabbcc001122/net", NetConfig{Mode: "ethernet", DHCP: false, IP: "10.0.0.50", Mask: "255.255.255.0", Gateway: "10.0.0.1"}, nil, 200)
	eventually(t, "netcfg", func() bool { return bp2Rec.has(`"t":"netcfg","mode":"ethernet","dhcp":false,"ip":"10.0.0.50"`) })

	// Admin sees battery.
	bp2.Send(proto.Telemetry{T: "telemetry", Battery: 77, Net: "ethernet", PoE: true, IP: "10.0.0.50"})
	eventually(t, "telemetry", func() bool {
		h.do("GET", "/api/devices", nil, &devs, 200)
		for _, d := range devs {
			if d.Telemetry != nil && d.Telemetry.Battery == 77 && d.UserName == "Camera 1" {
				return true
			}
		}
		return false
	})
}

func TestConfigUpdateRebuildsSessions(t *testing.T) {
	h := newHarness(t)
	_, camRec := h.connect(proto.Msg{User: "Camera 1", PIN: "2222", Client: "test"})
	var cfg Config
	h.do("GET", "/api/config", nil, &cfg, 200)
	cfg.Channels[1].Name = "Cameras"
	h.do("PUT", "/api/config", cfg, nil, 200)
	eventually(t, "re-welcome", func() bool { return camRec.has(`"name":"Cameras"`) })
	// Duplicate channel ids rejected.
	cfg.Channels = append(cfg.Channels, cfg.Channels[0])
	h.do("PUT", "/api/config", cfg, nil, 400)
}

func TestTallyScreenControl(t *testing.T) {
	h := newHarness(t)
	cam, camRec := h.connect(proto.Msg{User: "Camera 1", PIN: "2222", Client: "web"})
	// Client reports its view; admin sees it.
	on := true
	cam.Send(proto.Msg{T: "view", On: &on})
	eventually(t, "view reported", func() bool {
		var ss []SessionInfo
		h.do("GET", "/api/sessions", nil, &ss, 200)
		return len(ss) == 1 && ss[0].TallyView
	})
	// Admin switches it remotely.
	h.do("POST", "/api/sessions/1/remote", RemoteOp{Op: "tallyscreen", On: false}, nil, 200)
	eventually(t, "remote tallyscreen", func() bool { return camRec.has(`"op":"tallyscreen"`) })
	// Per-user default is carried in the welcome.
	var cfg Config
	h.do("GET", "/api/config", nil, &cfg, 200)
	for i := range cfg.Users {
		if cfg.Users[i].Name == "Camera 1" {
			cfg.Users[i].TallyScreen = true
		}
	}
	h.do("PUT", "/api/config", cfg, nil, 200)
	eventually(t, "welcome tallyScreen", func() bool { return camRec.has(`"tallyScreen":true`) })
}

func TestPersonNames(t *testing.T) {
	h := newHarness(t)
	// Admin sets a default person for Camera 1.
	var cfg Config
	h.do("GET", "/api/config", nil, &cfg, 200)
	for i := range cfg.Users {
		if cfg.Users[i].Name == "Camera 1" {
			cfg.Users[i].Person = "  John   Doe "
		}
	}
	h.do("PUT", "/api/config", cfg, nil, 200)

	cam, camRec := h.connect(proto.Msg{User: "Camera 1", PIN: "2222", Client: "web"})
	eventually(t, "default person in welcome", func() bool { return camRec.has(`"person":"John Doe"`) })
	_, dirRec := h.connect(proto.Msg{User: "Director", PIN: "1111", Client: "web", Name: "Jane Roe"})

	persons := func() map[string]string {
		var ss []SessionInfo
		h.do("GET", "/api/sessions", nil, &ss, 200)
		m := map[string]string{}
		for _, s := range ss {
			m[s.User] = s.Person
		}
		return m
	}
	if p := persons(); p["Camera 1"] != "John Doe" || p["Director"] != "Jane Roe" {
		t.Fatalf("persons = %v", p)
	}
	// The device renames itself; it wins over the admin default and others see it.
	cam.Send(proto.Msg{T: "name", Name: "Sam Smith (sub)"})
	eventually(t, "rename", func() bool { return persons()["Camera 1"] == "Sam Smith (sub)" })
	eventually(t, "others see person", func() bool { return dirRec.has(`"name":"Camera 1","person":"Sam Smith (sub)"`) })
	// Long names are capped at 40 characters.
	cam.Send(proto.Msg{T: "name", Name: strings.Repeat("x", 80)})
	eventually(t, "capped", func() bool { return len(persons()["Camera 1"]) == 40 })
}

// Without the admin's new-beltpack window, a device id alone gets nobody in,
// and nothing is saved.
func TestBeltpackNeedsPairingWindow(t *testing.T) {
	h := newHarness(t)
	if _, err := client.Dial(context.Background(), h.ts.URL, proto.Msg{Client: "beltpack", Device: "aabbcc001122"}); err == nil {
		t.Fatal("anonymous beltpack accepted with the window closed")
	}
	h.do("POST", "/api/pairing", map[string]int{"minutes": 5}, nil, 200)
	h.connect(proto.Msg{Client: "beltpack", Device: "aabbcc001122"})
	// a second connection claiming the same id is refused
	if _, err := client.Dial(context.Background(), h.ts.URL, proto.Msg{Client: "beltpack", Device: "aabbcc001122"}); err == nil {
		t.Fatal("second connection with the same device id accepted")
	}
	h.do("POST", "/api/pairing", map[string]int{"minutes": 0}, nil, 200)
	if len(h.srv.Store.Get().Devices) != 0 {
		t.Fatal("anonymous beltpack was saved to the config")
	}
}

// Someone who only knows an assigned beltpack's id must not be able to take its
// place and receive the Wi-Fi password or a user token meant for it.
func TestBeltpackIDCannotBeHijacked(t *testing.T) {
	h := newHarness(t)
	cfg := h.srv.Store.Get()
	cam, a1 := cfg.Users[1], cfg.Users[2]
	real, _ := h.connect(proto.Msg{Client: "beltpack", Device: "aabbcc001122", Token: cam.Token})
	real.Close()
	eventually(t, "real device gone", func() bool { return len(h.srv.Hub.Sessions()) == 0 })

	h.do("POST", "/api/pairing", map[string]int{"minutes": 5}, nil, 200)
	_, atkRec := h.connect(proto.Msg{Client: "beltpack", Device: "aabbcc001122"})
	h.do("PUT", "/api/devices/aabbcc001122/net", NetConfig{Mode: "wifi", DHCP: true, SSID: "Venue", Pass: "secret-pass"}, nil, 200)
	time.Sleep(100 * time.Millisecond)
	if atkRec.has("secret-pass") {
		t.Fatal("Wi-Fi password sent to a connection without the device's token")
	}
	// another user's token can't claim it either
	if _, err := client.Dial(context.Background(), h.ts.URL, proto.Msg{Client: "beltpack", Device: "aabbcc001122", Token: a1.Token}); err == nil {
		t.Fatal("another user's token took over an assigned beltpack")
	}
	// a name + PIN login can't rebind a beltpack to itself
	h.connect(proto.Msg{User: "A1", PIN: "3333", Client: "web", Device: "aabbcc001122"})
	time.Sleep(50 * time.Millisecond)
	if cfg := h.srv.Store.Get(); cfg.device("aabbcc001122") == nil || cfg.device("aabbcc001122").UserID != cam.ID {
		t.Fatal("device rebound by a PIN login")
	}
}

// Behind the Cloudflare tunnel every request comes from the cloudflared
// container: the real client IP must come from CF-Connecting-IP, so one
// attacker's failed logins don't lock everybody out.
func TestClientIPBehindTrustedProxy(t *testing.T) {
	srv := New(nil, nil)
	req := func(peer, cf, xff string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = peer + ":5555"
		if cf != "" {
			r.Header.Set("CF-Connecting-IP", cf)
		}
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}
	if ip := srv.clientIP(req("172.18.0.3", "203.0.113.9", "")); ip != "172.18.0.3" {
		t.Fatalf("untrusted peer's header believed: %s", ip)
	}
	srv.TrustProxies([]string{"172.18.0.0/16"})
	if ip := srv.clientIP(req("172.18.0.3", "203.0.113.9", "")); ip != "203.0.113.9" {
		t.Fatalf("CF-Connecting-IP ignored: %s", ip)
	}
	// the caller can put anything first in X-Forwarded-For; only the last entry counts
	if ip := srv.clientIP(req("127.0.0.1", "", "6.6.6.6, 198.51.100.4")); ip != "198.51.100.4" {
		t.Fatalf("spoofable X-Forwarded-For entry used: %s", ip)
	}
}

func TestLoginLimitsPerName(t *testing.T) {
	h := newHarness(t)
	// different IPs (as through the tunnel), same name: blocked after nameFailLimit
	for i := 0; i < nameFailLimit; i++ {
		h.srv.limiter.fail("name:director", nameFailLimit)
	}
	if _, err := client.Dial(context.Background(), h.ts.URL, proto.Msg{User: "Director", PIN: "1111"}); err == nil {
		t.Fatal("correct PIN accepted while the name is blocked after many wrong guesses")
	}
	// short PINs can't open the admin page
	h.admin = ""
	h.do("POST", "/api/admin/login", map[string]string{"user": "Camera 1", "pin": "2222"}, nil, 401)
}

func TestAdminPINMinimum(t *testing.T) {
	h := newHarness(t)
	h.do("POST", "/api/admin/login", map[string]string{"user": "Director", "pin": "1111"}, nil, 403)
	var cfg Config
	h.do("GET", "/api/config", nil, &cfg, 200)
	cfg.Users[0].PIN = "482913"
	h.do("PUT", "/api/config", cfg, nil, 200)
	h.do("POST", "/api/admin/login", map[string]string{"user": "Director", "pin": "482913"}, nil, 200)
}

func TestSecurityHeadersAndNoTokenInURL(t *testing.T) {
	h := newHarness(t)
	resp, err := http.Get(h.ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("X-Frame-Options") != "DENY" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("missing anti-framing headers: %v", resp.Header)
	}
	resp, err = http.Get(h.ts.URL + "/api/qr?text=x&k=" + h.admin)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("admin token accepted in the URL: %d", resp.StatusCode)
	}
}
