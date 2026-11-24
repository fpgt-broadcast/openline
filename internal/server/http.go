package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	qrcode "github.com/skip2/go-qrcode"

	"openline/internal/proto"
)

// Server wires the hub, config store and HTTP handlers together.
type Server struct {
	Store *Store
	Hub   *Hub
	Web   fs.FS // static web client

	Version string // shown in the app and admin (GET /api/info)

	adminMu  sync.Mutex
	adminTok map[string]time.Time

	limiter *limiter

	// Proxies whose client-IP headers are believed (besides loopback), e.g. the
	// cloudflared container. IPs, CIDRs or host names (resolved every 30 s).
	proxies *proxyTrust

	connMu  sync.Mutex
	preAuth map[string]int // connections still waiting for their hello, per IP
}

// Limits for things anyone on the internet can do before signing in.
const (
	maxPreAuthPerIP = 10
	maxPreAuth      = 200
	ipFailLimit     = 8  // wrong logins from one IP before a 1 minute block
	nameFailLimit   = 20 // wrong logins for one name / the admin password before a 1 minute block
	minAdminPIN     = 6  // admin page sign-in with a PIN needs at least this many characters
)

func New(store *Store, web fs.FS) *Server {
	return &Server{
		Store:    store,
		Hub:      NewHub(store),
		Web:      web,
		adminTok: map[string]time.Time{},
		limiter:  &limiter{fails: map[string]*failRec{}},
		proxies:  &proxyTrust{},
		preAuth:  map[string]int{},
	}
}

// TrustProxies sets which reverse proxies may report the client's real IP
// (CF-Connecting-IP / X-Forwarded-For). Loopback is always trusted.
func (s *Server) TrustProxies(list []string) { s.proxies.set(list) }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.HandleFunc("GET /api/info", s.handleInfo)
	mux.HandleFunc("POST /api/admin/login", s.handleAdminLogin)
	mux.HandleFunc("GET /api/config", s.admin(s.handleGetConfig))
	mux.HandleFunc("PUT /api/config", s.admin(s.handlePutConfig))
	mux.HandleFunc("GET /api/sessions", s.admin(s.handleSessions))
	mux.HandleFunc("POST /api/sessions/{sid}/kick", s.admin(s.handleKick))
	mux.HandleFunc("POST /api/sessions/{sid}/remote", s.admin(s.handleRemote))
	mux.HandleFunc("GET /api/devices", s.admin(s.handleDevices))
	mux.HandleFunc("GET /api/pairing", s.admin(s.handleGetPairing))
	mux.HandleFunc("POST /api/pairing", s.admin(s.handleSetPairing))
	mux.HandleFunc("PUT /api/devices/{id}", s.admin(s.handlePutDevice))
	mux.HandleFunc("PUT /api/devices/{id}/net", s.admin(s.handleDeviceNet))
	mux.HandleFunc("DELETE /api/devices/{id}", s.admin(s.handleDeleteDevice))
	mux.HandleFunc("GET /api/qr", s.admin(s.handleQR))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	static := http.FileServerFS(s.Web)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		static.ServeHTTP(w, r)
	})
	return securityHeaders(mux)
}

// securityHeaders: no framing (clickjacking of the admin page), scripts only
// from this server, no referrer leaks.
func securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; " +
		"connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// ---------------- WebSocket ----------------

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if s.limiter.blocked(limitKey(ip)) {
		http.Error(w, "too many failed logins, try again in a minute", http.StatusTooManyRequests)
		return
	}
	if !s.enterPreAuth(ip) {
		http.Error(w, "too many connections", http.StatusTooManyRequests)
		return
	}
	preAuth := true
	defer func() {
		if preAuth {
			s.leavePreAuth(ip)
		}
	}()
	// Default accept options reject cross-origin browser connections;
	// devices and bridges send no Origin header and are allowed.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(16 << 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer c.CloseNow()

	// First message must be hello.
	hctx, hcancel := context.WithTimeout(ctx, 10*time.Second)
	typ, data, err := c.Read(hctx)
	hcancel()
	if err != nil || typ != websocket.MessageText {
		return
	}
	var hello proto.Msg
	if json.Unmarshal(data, &hello) != nil || hello.T != "hello" {
		c.Close(websocket.StatusPolicyViolation, "expected hello")
		return
	}
	nameKey := ""
	if hello.User != "" {
		nameKey = "name:" + strings.ToLower(strings.TrimSpace(hello.User))
	}
	if nameKey != "" && s.limiter.blocked(nameKey) {
		b, _ := json.Marshal(proto.Msg{T: "error", Msg: "too many failed logins for this name, try again in a minute"})
		c.Write(ctx, websocket.MessageText, b)
		c.Close(4002, "try again later")
		return
	}
	user, err := s.Hub.Admit(hello)
	if err != nil {
		msg, code := "login failed", websocket.StatusCode(4001)
		if errors.Is(err, ErrAuth) {
			s.limiter.fail(limitKey(ip), ipFailLimit)
			if nameKey != "" {
				s.limiter.fail(nameKey, nameFailLimit)
			}
		} else {
			msg, code = err.Error(), 4002 // beltpack not accepted right now: retry later
		}
		b, _ := json.Marshal(proto.Msg{T: "error", Msg: msg})
		c.Write(ctx, websocket.MessageText, b)
		c.Close(code, "login failed")
		return
	}
	s.limiter.ok(limitKey(ip))
	s.leavePreAuth(ip)
	preAuth = false
	sess := s.Hub.Join(user, hello, ip)
	defer sess.Leave()

	// Writer.
	go func() {
		defer cancel()
		ping := time.NewTicker(15 * time.Second)
		defer ping.Stop()
		for {
			select {
			case f := <-sess.Out():
				wctx, wc := context.WithTimeout(ctx, 5*time.Second)
				typ := websocket.MessageBinary
				if f.text {
					typ = websocket.MessageText
				}
				err := c.Write(wctx, typ, f.data)
				wc()
				if err != nil {
					return
				}
			case <-ping.C:
				pctx, pc := context.WithTimeout(ctx, 10*time.Second)
				err := c.Ping(pctx)
				pc()
				if err != nil {
					return
				}
			case <-sess.Done():
				c.Close(websocket.StatusNormalClosure, "bye")
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	// Reader. Audio is 50 packets/s and control messages a few per second, so
	// anything far beyond that is dropped, and a flood disconnects.
	var window time.Time
	var nAudio, nCtrl int
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if now := time.Now(); now.Sub(window) >= time.Second {
			window, nAudio, nCtrl = now, 0, 0
		}
		if typ == websocket.MessageBinary {
			if nAudio++; nAudio > 150 {
				if nAudio > 1000 {
					return
				}
				continue
			}
			if seq, opus, err := proto.DecodeUplink(data); err == nil && len(opus) <= proto.MaxOpusPacket {
				sess.HandleAudio(seq, opus)
			}
			continue
		}
		if nCtrl++; nCtrl > 30 {
			if nCtrl > 300 {
				return
			}
			continue
		}
		var m proto.Msg
		if json.Unmarshal(data, &m) == nil {
			sess.HandleControl(m, data)
		}
	}
}

// ---------------- REST ----------------

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	cfg := s.Store.Get()
	writeJSON(w, map[string]any{"server": cfg.ServerName, "publicURL": cfg.PublicURL, "protocol": proto.Version, "version": s.Version})
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if s.limiter.blocked(limitKey(ip)) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		Password string `json:"password"`
		User     string `json:"user"`
		PIN      string `json:"pin"`
		Token    string `json:"token"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	key := "admin-password"
	if req.Password == "" {
		key = "name:" + strings.ToLower(strings.TrimSpace(req.User))
	}
	if s.limiter.blocked(key) {
		http.Error(w, "too many attempts, try again in a minute", http.StatusTooManyRequests)
		return
	}
	ok := false
	if req.Password != "" {
		ok = s.Store.CheckAdmin(req.Password)
	} else if u, err := s.Hub.Authenticate(proto.Msg{User: req.User, PIN: req.PIN, Token: req.Token}); err == nil && u.Admin {
		if req.Token == "" && len(req.PIN) < minAdminPIN {
			http.Error(w, "admin sign-in with a PIN needs a PIN of at least 6 digits: use the admin password, or give this user a longer PIN", http.StatusForbidden)
			return
		}
		ok = true
	}
	if !ok {
		s.limiter.fail(limitKey(ip), ipFailLimit)
		s.limiter.fail(key, nameFailLimit)
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	s.limiter.ok(limitKey(ip))
	tok := randHex(24)
	s.adminMu.Lock()
	for k, exp := range s.adminTok {
		if time.Now().After(exp) {
			delete(s.adminTok, k)
		}
	}
	s.adminTok[tok] = time.Now().Add(12 * time.Hour)
	s.adminMu.Unlock()
	writeJSON(w, map[string]string{"token": tok})
}

func (s *Server) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		s.adminMu.Lock()
		exp, ok := s.adminTok[tok]
		s.adminMu.Unlock()
		if !ok || time.Now().After(exp) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.Store.Get()
	cfg.AdminPassHash = ""
	writeJSON(w, cfg)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Config
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.NewPassword != "" && len(req.NewPassword) < 6 {
		http.Error(w, "admin password must be at least 6 characters", http.StatusBadRequest)
		return
	}
	cfg, err := s.Store.Replace(req.Config, req.NewPassword)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.NewPassword != "" {
		// new admin password: sign out every other admin session
		mine := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		s.adminMu.Lock()
		for k := range s.adminTok {
			if k != mine {
				delete(s.adminTok, k)
			}
		}
		s.adminMu.Unlock()
	}
	s.Hub.ConfigChanged()
	cfg.AdminPassHash = ""
	writeJSON(w, cfg)
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Hub.Sessions())
}

func (s *Server) handleKick(w http.ResponseWriter, r *http.Request) {
	sid, err := strconv.Atoi(r.PathValue("sid"))
	if err != nil || !s.Hub.Kick(uint16(sid)) {
		http.Error(w, "no such session", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleRemote(w http.ResponseWriter, r *http.Request) {
	sid, err := strconv.Atoi(r.PathValue("sid"))
	if err != nil {
		http.Error(w, "bad sid", http.StatusBadRequest)
		return
	}
	var op RemoteOp
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&op); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := s.Hub.Remote(uint16(sid), op); err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, ErrNoSession) {
			code = http.StatusNotFound
		}
		http.Error(w, err.Error(), code)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Hub.Devices())
}

func (s *Server) handleGetPairing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, pairingInfo(s.Hub.Pairing()))
}

// handleSetPairing opens ({"minutes": 10}) or closes ({"minutes": 0}) the
// window in which new beltpacks may connect without a token.
func (s *Server) handleSetPairing(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Minutes int `json:"minutes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	until := s.Hub.SetPairing(time.Duration(clamp(req.Minutes, 0, 60)) * time.Minute)
	writeJSON(w, pairingInfo(until))
}

func pairingInfo(until time.Time) map[string]any {
	if until.IsZero() {
		return map[string]any{"open": false}
	}
	return map[string]any{"open": true, "until": until, "secondsLeft": int(time.Until(until).Seconds())}
}

func (s *Server) handlePutDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string      `json:"name"`
		UserID string      `json:"userId"`
		Audio  DeviceAudio `json:"audio"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	d, err := s.Hub.UpdateDevice(r.PathValue("id"), req.Name, req.UserID, req.Audio)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, d)
}

func (s *Server) handleDeviceNet(w http.ResponseWriter, r *http.Request) {
	var n NetConfig
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&n); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !n.DHCP && (net.ParseIP(n.IP) == nil || net.ParseIP(n.Mask) == nil) {
		http.Error(w, "static addressing needs a valid IP and subnet mask", http.StatusBadRequest)
		return
	}
	d, err := s.Hub.SetDeviceNet(r.PathValue("id"), n)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, d)
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.Hub.DeleteDevice(r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleQR(w http.ResponseWriter, r *http.Request) {
	text := r.URL.Query().Get("text")
	if text == "" || len(text) > 1024 {
		http.Error(w, "text required", http.StatusBadRequest)
		return
	}
	png, err := qrcode.Encode(text, qrcode.Medium, 320)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(v); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		log.Printf("write json: %v", err)
	}
}

// clientIP returns the caller's IP. Behind a trusted proxy (loopback, or one
// given with -trusted-proxy such as the cloudflared container) it uses
// CF-Connecting-IP, else the last X-Forwarded-For entry: the one the proxy
// itself added. Earlier entries can be made up by the caller.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if peer == nil || !(peer.IsLoopback() || s.proxies.trusted(peer)) {
		return host
	}
	if cf := net.ParseIP(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); cf != nil {
		return cf.String()
	}
	if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
		parts := strings.Split(xff[len(xff)-1], ",")
		if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); ip != nil {
			return ip.String()
		}
	}
	return host
}

type proxyTrust struct {
	mu       sync.Mutex
	nets     []*net.IPNet
	hosts    []string
	resolved []net.IP
	at       time.Time
}

func (p *proxyTrust) set(list []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nets, p.hosts, p.resolved, p.at = nil, nil, nil, time.Time{}
	for _, e := range list {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(e); err == nil {
			p.nets = append(p.nets, n)
		} else if ip := net.ParseIP(e); ip != nil {
			p.nets = append(p.nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(len(ip)*8, len(ip)*8)})
		} else {
			p.hosts = append(p.hosts, e)
		}
	}
}

func (p *proxyTrust) trusted(ip net.IP) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, n := range p.nets {
		if n.Contains(ip) {
			return true
		}
	}
	if len(p.hosts) > 0 && time.Since(p.at) > 30*time.Second {
		p.resolved, p.at = nil, time.Now()
		for _, h := range p.hosts {
			ips, _ := net.LookupIP(h)
			p.resolved = append(p.resolved, ips...)
		}
	}
	for _, r := range p.resolved {
		if r.Equal(ip) {
			return true
		}
	}
	return false
}

func (s *Server) enterPreAuth(ip string) bool {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	total := 0
	for _, n := range s.preAuth {
		total += n
	}
	if s.preAuth[ip] >= maxPreAuthPerIP || total >= maxPreAuth {
		return false
	}
	s.preAuth[ip]++
	return true
}

func (s *Server) leavePreAuth(ip string) {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if s.preAuth[ip]--; s.preAuth[ip] <= 0 {
		delete(s.preAuth, ip)
	}
}

// ---------------- login rate limiting ----------------

// limitKey is what failed logins are counted against: the IPv4 address, or
// for IPv6 the /64 block. One IPv6 user usually holds a whole /64 and could
// otherwise switch to a fresh address every few guesses.
func limitKey(ip string) string {
	p := net.ParseIP(ip)
	if p == nil || p.To4() != nil {
		return ip
	}
	return p.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

type failRec struct {
	n     int
	until time.Time
	last  time.Time
}

type limiter struct {
	mu    sync.Mutex
	fails map[string]*failRec
}

func (l *limiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.fails[ip]
	return f != nil && time.Now().Before(f.until)
}

func (l *limiter) fail(ip string, max int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.fails[ip]
	if f == nil || time.Since(f.last) > 10*time.Minute {
		f = &failRec{}
		l.fails[ip] = f
	}
	f.n++
	f.last = time.Now()
	if f.n >= max {
		f.until = time.Now().Add(time.Minute)
		f.n = 0
	}
	if len(l.fails) > 10000 { // bound memory
		for k, v := range l.fails {
			if time.Since(v.last) > 10*time.Minute {
				delete(l.fails, k)
			}
		}
	}
}

func (l *limiter) ok(ip string) {
	l.mu.Lock()
	delete(l.fails, ip)
	l.mu.Unlock()
}
