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

	adminMu  sync.Mutex
	adminTok map[string]time.Time

	limiter *limiter
}

func New(store *Store, web fs.FS) *Server {
	return &Server{
		Store:    store,
		Hub:      NewHub(store),
		Web:      web,
		adminTok: map[string]time.Time{},
		limiter:  &limiter{fails: map[string]*failRec{}},
	}
}

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
	mux.HandleFunc("PUT /api/devices/{id}", s.admin(s.handlePutDevice))
	mux.HandleFunc("PUT /api/devices/{id}/net", s.admin(s.handleDeviceNet))
	mux.HandleFunc("DELETE /api/devices/{id}", s.admin(s.handleDeleteDevice))
	mux.HandleFunc("GET /api/qr", s.admin(s.handleQR))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	static := http.FileServerFS(s.Web)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		static.ServeHTTP(w, r)
	})
	return mux
}

// ---------------- WebSocket ----------------

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.limiter.blocked(ip) {
		http.Error(w, "too many failed logins, try again in a minute", http.StatusTooManyRequests)
		return
	}
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
	user, err := s.Hub.Authenticate(hello)
	if err != nil {
		s.limiter.fail(ip)
		b, _ := json.Marshal(proto.Msg{T: "error", Msg: "login failed"})
		c.Write(ctx, websocket.MessageText, b)
		c.Close(4001, "login failed")
		return
	}
	s.limiter.ok(ip)
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

	// Reader.
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			if seq, opus, err := proto.DecodeUplink(data); err == nil && len(opus) <= proto.MaxOpusPacket {
				sess.HandleAudio(seq, opus)
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
	writeJSON(w, map[string]any{"server": cfg.ServerName, "publicURL": cfg.PublicURL, "protocol": proto.Version})
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.limiter.blocked(ip) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
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
	ok := false
	if req.Password != "" {
		ok = s.Store.CheckAdmin(req.Password)
	} else if u, err := s.Hub.Authenticate(proto.Msg{User: req.User, PIN: req.PIN, Token: req.Token}); err == nil && u.Admin {
		ok = true
	}
	if !ok {
		s.limiter.fail(ip)
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	s.limiter.ok(ip)
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
		if tok == "" {
			tok = r.URL.Query().Get("k")
		}
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
	if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
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

func (s *Server) handlePutDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string      `json:"name"`
		UserID string      `json:"userId"`
		Audio  DeviceAudio `json:"audio"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
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

// clientIP returns the remote IP, honouring X-Forwarded-For only when the
// direct peer is loopback (i.e. a local reverse proxy / tunnel agent).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	return host
}

// ---------------- login rate limiting ----------------

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

func (l *limiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.fails[ip]
	if f == nil || time.Since(f.last) > 10*time.Minute {
		f = &failRec{}
		l.fails[ip] = f
	}
	f.n++
	f.last = time.Now()
	if f.n >= 8 {
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
