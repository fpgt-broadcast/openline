package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"openline/internal/proto"
)

type outFrame struct {
	text bool
	data []byte
}

// Session is one connected client (browser tab, beltpack, bridge link...).
type Session struct {
	SID       uint16
	Client    string
	Remote    string
	DeviceID  string // beltpacks only
	Connected time.Time

	hub  *Hub
	out  chan outFrame
	done chan struct{}
	once sync.Once

	// guarded by hub.mu
	user      User // ID == "" for an unassigned (pending) beltpack
	slots     []slot
	slotIdx   map[int]int // channel id -> index in slots (mask bit)
	talk      map[int]bool
	listen    map[int]bool
	call      map[int]bool
	muted     bool
	tally     string // program | preview | off
	lastRx    time.Time
	telemetry *proto.Telemetry
	telemAt   time.Time
}

type slot struct {
	ch        Channel
	canTalk   bool
	canListen bool
}

// Hub routes audio and control between sessions.
type Hub struct {
	store *Store

	mu       sync.RWMutex
	sessions map[uint16]*Session
	nextSID  uint16

	tallyPgm, tallyPvw map[int]bool // switcher inputs on program / preview
}

func NewHub(store *Store) *Hub {
	return &Hub{store: store, sessions: map[uint16]*Session{}, tallyPgm: map[int]bool{}, tallyPvw: map[int]bool{}}
}

var ErrAuth = errors.New("login failed")

// Authenticate resolves a hello message to a user. A beltpack that sends only
// its device id (no token) is accepted as an unassigned device so an admin
// can adopt it; it gets no channels until assigned.
func (h *Hub) Authenticate(m proto.Msg) (User, error) {
	cfg := h.store.Get()
	if m.Token != "" {
		var found *User
		for i := range cfg.Users {
			if subtle.ConstantTimeCompare([]byte(cfg.Users[i].Token), []byte(m.Token)) == 1 {
				found = &cfg.Users[i]
			}
		}
		if found != nil {
			return *found, nil
		}
		return User{}, ErrAuth
	}
	if m.User != "" {
		name := strings.ToLower(strings.TrimSpace(m.User))
		for _, u := range cfg.Users {
			if strings.ToLower(u.Name) == name && u.PIN != "" &&
				subtle.ConstantTimeCompare([]byte(u.PIN), []byte(m.PIN)) == 1 {
				return u, nil
			}
		}
		return User{}, ErrAuth
	}
	if m.Device != "" && m.Client == "beltpack" {
		return User{Name: "Unassigned"}, nil
	}
	return User{}, ErrAuth
}

// Join registers a new session for an authenticated user.
func (h *Hub) Join(u User, hello proto.Msg, remote string) *Session {
	client := hello.Client
	if client == "" {
		client = "unknown"
	}
	s := &Session{
		Client:    client,
		Remote:    remote,
		DeviceID:  normDeviceID(hello.Device),
		Connected: time.Now(),
		hub:       h,
		out:       make(chan outFrame, 512),
		done:      make(chan struct{}),
		talk:      map[int]bool{},
		listen:    map[int]bool{},
		call:      map[int]bool{},
	}
	var dev *Device
	if s.DeviceID != "" {
		cfg, err := h.store.Update(func(c *Config) error {
			d := c.device(s.DeviceID)
			if d == nil {
				c.Devices = append(c.Devices, Device{ID: s.DeviceID, Net: NetConfig{Mode: "wifi", DHCP: true}, Audio: defaultDeviceAudio()})
				d = &c.Devices[len(c.Devices)-1]
			}
			if u.ID != "" {
				d.UserID = u.ID
			}
			return nil
		})
		if err == nil {
			dev = cfg.device(s.DeviceID)
			if u.ID == "" && dev != nil {
				u.Name = dev.Name + " (unassigned)"
			}
		}
	}
	cfg := h.store.Get()
	h.mu.Lock()
	for {
		h.nextSID++
		if h.nextSID == 0 {
			h.nextSID = 1
		}
		if _, used := h.sessions[h.nextSID]; !used {
			break
		}
	}
	s.SID = h.nextSID
	s.applyUserLocked(u, &cfg)
	for _, sl := range s.slots {
		if sl.canListen {
			s.listen[sl.ch.ID] = true
		}
	}
	h.sessions[s.SID] = s
	h.mu.Unlock()

	log.Printf("join sid=%d user=%q client=%s device=%s from %s", s.SID, u.Name, client, s.DeviceID, remote)
	s.sendWelcome()
	if dev != nil {
		s.sendJSON(settingsMsg(dev))
	}
	h.mu.Lock()
	s.tally = ""
	h.updateTallyLocked(s)
	h.mu.Unlock()
	h.broadcastState()
	return s
}

func settingsMsg(d *Device) proto.Settings {
	return proto.Settings{T: "settings", Name: d.Name, MicType: d.Audio.MicType, MicGain: d.Audio.MicGain,
		Sidetone: d.Audio.Sidetone, HPLevel: d.Audio.HPLevel, Bright: d.Audio.Bright}
}

func normDeviceID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	id = strings.NewReplacer(":", "", "-", "").Replace(id)
	if len(id) > 32 {
		id = id[:32]
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return ""
		}
	}
	return id
}

// applyUserLocked (re)builds the slot list from the user and config.
func (s *Session) applyUserLocked(u User, cfg *Config) {
	s.user = u
	s.slots = s.slots[:0]
	s.slotIdx = map[int]int{}
	for _, a := range u.Channels {
		ch := cfg.channel(a.Ch)
		if ch == nil {
			continue
		}
		canTalk := a.Talk && (!ch.ListenOnly || u.Bridge)
		s.slotIdx[ch.ID] = len(s.slots)
		s.slots = append(s.slots, slot{ch: *ch, canTalk: canTalk, canListen: a.Listen})
	}
	for id := range s.talk {
		if i, ok := s.slotIdx[id]; !ok || !s.slots[i].canTalk {
			delete(s.talk, id)
		}
	}
	for id := range s.listen {
		if i, ok := s.slotIdx[id]; !ok || !s.slots[i].canListen {
			delete(s.listen, id)
		}
	}
	for id := range s.call {
		if _, ok := s.slotIdx[id]; !ok {
			delete(s.call, id)
		}
	}
}

func (s *Session) Leave() {
	s.once.Do(func() {
		close(s.done)
		h := s.hub
		h.mu.Lock()
		delete(h.sessions, s.SID)
		h.mu.Unlock()
		log.Printf("leave sid=%d user=%q", s.SID, s.user.Name)
		h.broadcastState()
	})
}

// Done is closed when the session should terminate.
func (s *Session) Done() <-chan struct{} { return s.done }

// Out yields frames to write to the socket.
func (s *Session) Out() <-chan outFrame { return s.out }

func (s *Session) sendJSON(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	select {
	case s.out <- outFrame{text: true, data: b}:
	case <-s.done:
	default:
		log.Printf("sid=%d send queue full, disconnecting", s.SID)
		go s.Leave()
	}
}

func (s *Session) sendAudio(b []byte) {
	select {
	case s.out <- outFrame{data: b}:
	default: // drop audio rather than add latency
	}
}

func (s *Session) Error(msg string) { s.sendJSON(proto.Msg{T: "error", Msg: msg}) }

func (s *Session) sendWelcome() {
	h := s.hub
	cfg := h.store.Get()
	h.mu.RLock()
	w := proto.Welcome{
		T:      "welcome",
		SID:    s.SID,
		Server: cfg.ServerName,
		User:   s.user.Name,
		Admin:  s.user.Admin,
		Talk:   keys(s.talk),
		Listen: keys(s.listen),
		Muted:  s.muted,
	}
	for _, sl := range s.slots {
		w.Channels = append(w.Channels, proto.ChannelInfo{
			ID: sl.ch.ID, Name: sl.ch.Name, Color: sl.ch.Color,
			CanTalk: sl.canTalk, CanListen: sl.canListen,
			ListenOnly: sl.ch.ListenOnly, Program: sl.ch.Program,
		})
	}
	h.mu.RUnlock()
	if w.Channels == nil {
		w.Channels = []proto.ChannelInfo{}
	}
	s.sendJSON(w)
}

func (s *Session) sendSelfLocked() {
	s.sendJSON(proto.Self{T: "self", Talk: keys(s.talk), Listen: keys(s.listen), Muted: s.muted})
}

// HandleControl processes one JSON control message from the client.
func (s *Session) HandleControl(m proto.Msg, raw []byte) {
	switch m.T {
	case "ping":
		s.sendJSON(proto.Msg{T: "pong", TS: m.TS})
	case "telemetry":
		var t proto.Telemetry
		if json.Unmarshal(raw, &t) == nil {
			s.hub.mu.Lock()
			s.telemetry = &t
			s.telemAt = time.Now()
			s.hub.mu.Unlock()
		}
	case "tally":
		var t proto.TallyUpdate
		if json.Unmarshal(raw, &t) == nil {
			s.hub.setTally(s, t)
		}
	case "talk", "listen", "call":
		on := m.On != nil && *m.On
		if err := s.hub.setState(s, m.T, m.Ch, on, false); err != nil {
			s.Error(err.Error())
		}
	}
}

// setState changes talk/listen/call for a session. force=true is an admin
// action (bypasses the admin mic mute, still honours channel permissions).
func (h *Hub) setState(s *Session, kind string, ch int, on, force bool) error {
	h.mu.Lock()
	i, ok := s.slotIdx[ch]
	if !ok {
		h.mu.Unlock()
		return errors.New("unknown channel")
	}
	sl := s.slots[i]
	var m map[int]bool
	switch kind {
	case "talk":
		if on && !sl.canTalk {
			s.sendSelfLocked()
			h.mu.Unlock()
			return errors.New("not allowed to talk on " + sl.ch.Name)
		}
		if on && s.muted && !force {
			s.sendSelfLocked()
			h.mu.Unlock()
			return errors.New("your mic is muted by the admin")
		}
		m = s.talk
	case "listen":
		if on && !sl.canListen {
			h.mu.Unlock()
			return errors.New("not allowed to listen to " + sl.ch.Name)
		}
		m = s.listen
	case "call":
		m = s.call
	default:
		h.mu.Unlock()
		return errors.New("bad state")
	}
	changed := m[ch] != on
	setBool(m, ch, on)
	s.sendSelfLocked()
	h.mu.Unlock()
	if changed {
		h.broadcastState()
	}
	return nil
}

// HandleAudio forwards one uplink Opus packet to every session that listens
// to at least one channel this session is talking on. No server-side mixing:
// each listener receives the individual streams and mixes them locally, which
// keeps server latency at ~0 and gives mix-minus for free.
func (s *Session) HandleAudio(seq uint16, opus []byte) {
	h := s.hub
	h.mu.RLock()
	defer h.mu.RUnlock()
	s.lastRx = time.Now()
	if len(s.talk) == 0 || s.muted {
		return
	}
	for _, l := range h.sessions {
		if l == s {
			continue
		}
		var mask uint32
		for ch := range s.talk {
			if l.listen[ch] {
				if idx, ok := l.slotIdx[ch]; ok {
					mask |= 1 << uint(idx)
				}
			}
		}
		if mask != 0 {
			l.sendAudio(proto.EncodeDownlink(s.SID, seq, mask, opus))
		}
	}
}

// setTally stores the switcher tally (only bridge users may publish it) and
// pushes each session its own LIVE / PREVIEW / off state when it changes.
func (h *Hub) setTally(from *Session, t proto.TallyUpdate) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !from.user.Bridge {
		log.Printf("tally from %q ignored: give that user the \"NDI bridge\" role", from.user.Name)
		from.sendJSON(proto.Msg{T: "error", Msg: "tally ignored: user " + from.user.Name + " needs the NDI bridge role"})
		return
	}
	h.tallyPgm, h.tallyPvw = map[int]bool{}, map[int]bool{}
	for _, n := range t.Program {
		h.tallyPgm[n] = true
	}
	for _, n := range t.Preview {
		h.tallyPvw[n] = true
	}
	for _, s := range h.sessions {
		h.updateTallyLocked(s)
	}
}

func (h *Hub) updateTallyLocked(s *Session) {
	state := "off"
	for _, n := range s.user.Tally {
		if h.tallyPgm[n] {
			state = "program"
			break
		}
		if h.tallyPvw[n] {
			state = "preview"
		}
	}
	if state != s.tally {
		s.tally = state
		s.sendJSON(proto.Tally{T: "tally", State: state})
	}
}

// broadcastState sends channel activity to every session.
func (h *Hub) broadcastState() {
	h.mu.RLock()
	defer h.mu.RUnlock()
	type agg struct{ talkers, calls, members []proto.Talker }
	all := map[int]*agg{}
	get := func(id int) *agg {
		a := all[id]
		if a == nil {
			a = &agg{talkers: []proto.Talker{}, calls: []proto.Talker{}, members: []proto.Talker{}}
			all[id] = a
		}
		return a
	}
	sids := make([]int, 0, len(h.sessions))
	for sid := range h.sessions {
		sids = append(sids, int(sid))
	}
	sort.Ints(sids)
	for _, sid := range sids {
		s := h.sessions[uint16(sid)]
		t := proto.Talker{SID: s.SID, Name: s.user.Name}
		for ch := range s.talk {
			get(ch).talkers = append(get(ch).talkers, t)
		}
		for ch := range s.call {
			get(ch).calls = append(get(ch).calls, t)
		}
		for ch := range s.listen {
			get(ch).members = append(get(ch).members, t)
		}
	}
	for _, s := range h.sessions {
		st := proto.State{T: "state", Channels: []proto.ChannelState{}}
		for _, sl := range s.slots {
			a := get(sl.ch.ID)
			st.Channels = append(st.Channels, proto.ChannelState{
				ID: sl.ch.ID, Talkers: a.talkers, Calls: a.calls, Members: a.members,
			})
		}
		s.sendJSON(st)
	}
}

// ConfigChanged re-applies the config to every live session.
func (h *Hub) ConfigChanged() {
	cfg := h.store.Get()
	byID := map[string]User{}
	for _, u := range cfg.Users {
		byID[u.ID] = u
	}
	var gone, keep []*Session
	h.mu.Lock()
	for _, s := range h.sessions {
		if s.user.ID == "" { // unassigned beltpack
			continue
		}
		u, ok := byID[s.user.ID]
		if !ok {
			gone = append(gone, s)
			continue
		}
		s.applyUserLocked(u, &cfg)
		h.updateTallyLocked(s)
		keep = append(keep, s)
	}
	h.mu.Unlock()
	for _, s := range gone {
		s.Error("user removed")
		s.Leave()
	}
	for _, s := range keep {
		s.sendWelcome()
	}
	h.broadcastState()
}

// ---------------- admin / remote control ----------------

// SessionInfo is the admin view of a live session.
type SessionInfo struct {
	SID       uint16           `json:"sid"`
	User      string           `json:"user"`
	UserID    string           `json:"userId"`
	Client    string           `json:"client"`
	Remote    string           `json:"remote"`
	Device    string           `json:"device,omitempty"`
	Connected time.Time        `json:"connected"`
	Channels  []int            `json:"channels"`
	Talk      []int            `json:"talk"`
	Listen    []int            `json:"listen"`
	Call      []int            `json:"call"`
	Muted     bool             `json:"muted"`
	Tally     string           `json:"tally"`
	Sending   bool             `json:"sending"`
	Telemetry *proto.Telemetry `json:"telemetry,omitempty"`
}

func (h *Hub) Sessions() []SessionInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := []SessionInfo{}
	for _, s := range h.sessions {
		out = append(out, s.infoLocked())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SID < out[j].SID })
	return out
}

func (s *Session) infoLocked() SessionInfo {
	chs := make([]int, 0, len(s.slots))
	for _, sl := range s.slots {
		chs = append(chs, sl.ch.ID)
	}
	return SessionInfo{
		SID: s.SID, User: s.user.Name, UserID: s.user.ID, Client: s.Client, Remote: s.Remote,
		Device: s.DeviceID, Connected: s.Connected, Channels: chs,
		Talk: keys(s.talk), Listen: keys(s.listen), Call: keys(s.call), Muted: s.muted, Tally: s.tally,
		Sending:   len(s.talk) > 0 && time.Since(s.lastRx) < 500*time.Millisecond,
		Telemetry: s.telemetry,
	}
}

func (h *Hub) session(sid uint16) *Session {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessions[sid]
}

func (h *Hub) deviceSession(id string) *Session {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.sessions {
		if s.DeviceID == id {
			return s
		}
	}
	return nil
}

func (h *Hub) Kick(sid uint16) bool {
	s := h.session(sid)
	if s == nil {
		return false
	}
	s.Error("disconnected by admin")
	s.Leave()
	return true
}

// RemoteOp is an admin action on a live session.
type RemoteOp struct {
	Op string `json:"op"` // talk | listen | call | mute | vol | identify | reboot | kick
	Ch int    `json:"ch,omitempty"`
	On bool   `json:"on,omitempty"`
	V  int    `json:"v,omitempty"`
}

var ErrNoSession = errors.New("no such session")

func (h *Hub) Remote(sid uint16, op RemoteOp) error {
	s := h.session(sid)
	if s == nil {
		return ErrNoSession
	}
	switch op.Op {
	case "talk", "listen", "call":
		return h.setState(s, op.Op, op.Ch, op.On, true)
	case "mute":
		h.mu.Lock()
		s.muted = op.On
		if op.On {
			for k := range s.talk {
				delete(s.talk, k)
			}
		}
		s.sendSelfLocked()
		h.mu.Unlock()
		on := op.On
		s.sendJSON(proto.Msg{T: "remote", Op: "mute", On: &on})
		h.broadcastState()
	case "vol":
		v := clamp(op.V, 0, 100)
		s.sendJSON(proto.Msg{T: "remote", Op: "vol", Ch: op.Ch, V: &v})
	case "identify", "reboot":
		s.sendJSON(proto.Msg{T: "remote", Op: op.Op})
	case "kick":
		h.Kick(sid)
	default:
		return errors.New("unknown op")
	}
	return nil
}

// DeviceInfo is the admin view of a beltpack.
type DeviceInfo struct {
	Device
	UserName  string           `json:"userName,omitempty"`
	Online    bool             `json:"online"`
	SID       uint16           `json:"sid,omitempty"`
	Remote    string           `json:"remote,omitempty"`
	Telemetry *proto.Telemetry `json:"telemetry,omitempty"`
	Session   *SessionInfo     `json:"session,omitempty"`
}

func (h *Hub) Devices() []DeviceInfo {
	cfg := h.store.Get()
	h.mu.RLock()
	defer h.mu.RUnlock()
	live := map[string]*Session{}
	for _, s := range h.sessions {
		if s.DeviceID != "" {
			live[s.DeviceID] = s
		}
	}
	out := []DeviceInfo{}
	for _, d := range cfg.Devices {
		d.Net.Pass = ""
		di := DeviceInfo{Device: d}
		if u := cfg.userByID(d.UserID); u != nil {
			di.UserName = u.Name
		}
		if s := live[d.ID]; s != nil {
			info := s.infoLocked()
			di.Online, di.SID, di.Remote, di.Telemetry, di.Session = true, s.SID, s.Remote, s.telemetry, &info
		}
		out = append(out, di)
	}
	return out
}

// UpdateDevice saves name/user/audio settings and pushes them live.
func (h *Hub) UpdateDevice(id string, name, userID string, audio DeviceAudio) (Device, error) {
	var userChanged bool
	cfg, err := h.store.Update(func(c *Config) error {
		d := c.device(id)
		if d == nil {
			return errors.New("no such device")
		}
		if userID != "" && c.userByID(userID) == nil {
			return errors.New("no such user")
		}
		userChanged = d.UserID != userID
		d.Name = strings.TrimSpace(name)
		d.UserID = userID
		d.Audio = audio
		return nil
	})
	if err != nil {
		return Device{}, err
	}
	d := *cfg.device(id)
	if s := h.deviceSession(id); s != nil {
		s.sendJSON(settingsMsg(&d))
		if userChanged {
			if u := cfg.userByID(userID); u != nil {
				// Device stores the token and reconnects as the new user.
				s.sendJSON(proto.Assign{T: "assign", Token: u.Token, User: u.Name})
			} else {
				s.sendJSON(proto.Assign{T: "assign", Token: "", User: ""})
			}
		}
	}
	d.Net.Pass = ""
	return d, nil
}

// SetDeviceNet stores network settings and pushes them (device reboots).
func (h *Hub) SetDeviceNet(id string, n NetConfig) (Device, error) {
	cfg, err := h.store.Update(func(c *Config) error {
		d := c.device(id)
		if d == nil {
			return errors.New("no such device")
		}
		if n.Pass == "" {
			n.Pass = d.Net.Pass // keep existing wifi password
		}
		d.Net = n
		return nil
	})
	if err != nil {
		return Device{}, err
	}
	d := *cfg.device(id)
	if s := h.deviceSession(id); s != nil {
		s.sendJSON(proto.NetCfg{T: "netcfg", Mode: d.Net.Mode, DHCP: d.Net.DHCP, IP: d.Net.IP, Mask: d.Net.Mask,
			Gateway: d.Net.Gateway, DNS: d.Net.DNS, SSID: d.Net.SSID, Pass: d.Net.Pass, Server: d.Net.Server})
	}
	d.Net.Pass = ""
	return d, nil
}

func (h *Hub) DeleteDevice(id string) error {
	_, err := h.store.Update(func(c *Config) error {
		out := c.Devices[:0]
		for _, d := range c.Devices {
			if d.ID != id {
				out = append(out, d)
			}
		}
		c.Devices = out
		return nil
	})
	return err
}

func setBool(m map[int]bool, k int, v bool) {
	if v {
		m[k] = true
	} else {
		delete(m, k)
	}
}

func keys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
