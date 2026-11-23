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
	tallyView bool   // client reports it is showing the full-screen tally view
	devPerson string // name the device entered for itself ("" = use the user's Person)
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

	// pairUntil: new (unassigned) beltpacks may connect without a token only
	// until this time. The admin opens the window from the Devices tab.
	pairUntil time.Time
}

// maxPending caps how many unassigned beltpacks can be connected at once.
const maxPending = 16

var ErrPairingClosed = errors.New("new beltpacks are not accepted right now: open \"Accept new beltpacks\" in admin > Beltpacks")

// SetPairing opens the new-beltpack window for d (0 closes it) and returns
// when it closes.
func (h *Hub) SetPairing(d time.Duration) time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	if d <= 0 {
		h.pairUntil = time.Time{}
	} else {
		h.pairUntil = time.Now().Add(d)
	}
	return h.pairUntil
}

// Pairing returns when the new-beltpack window closes (zero if closed).
func (h *Hub) Pairing() time.Time {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if time.Now().After(h.pairUntil) {
		return time.Time{}
	}
	return h.pairUntil
}

func NewHub(store *Store) *Hub {
	return &Hub{store: store, sessions: map[uint16]*Session{}, tallyPgm: map[int]bool{}, tallyPvw: map[int]bool{}}
}

var ErrAuth = errors.New("login failed")

// Admit checks a WebSocket hello. Besides name + PIN and token logins, a
// beltpack that sends only its device id (no token) is let in as an unassigned
// device so an admin can adopt it, but only while the admin has the
// new-beltpack window open, only one connection per device id, and at most
// maxPending at once. A token login that names a device must be the user that
// device is assigned to, so nobody can take over a known beltpack's slot (and
// the token or Wi-Fi password the admin later sends it).
func (h *Hub) Admit(m proto.Msg) (User, error) {
	dev := normDeviceID(m.Device)
	if m.Token == "" && m.User == "" {
		if dev == "" || m.Client != "beltpack" {
			return User{}, ErrAuth
		}
		h.mu.RLock()
		defer h.mu.RUnlock()
		if time.Now().After(h.pairUntil) {
			return User{}, ErrPairingClosed
		}
		pending := 0
		for _, s := range h.sessions {
			if s.user.ID == "" {
				pending++
				if s.DeviceID == dev {
					return User{}, errors.New("this beltpack is already connected")
				}
			}
		}
		if pending >= maxPending {
			return User{}, errors.New("too many new beltpacks waiting: assign or remove some in admin > Beltpacks")
		}
		return User{Name: "Unassigned"}, nil
	}
	u, err := h.Authenticate(m)
	if err != nil {
		return u, err
	}
	if dev != "" && m.Token != "" {
		cfg := h.store.Get()
		if d := cfg.device(dev); d != nil && d.UserID != "" && d.UserID != u.ID {
			return User{}, ErrAuth
		}
	}
	return u, nil
}

// Authenticate resolves name + PIN or a token to a user.
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
	return User{}, ErrAuth
}

// Join registers a new session for an authenticated user.
func (h *Hub) Join(u User, hello proto.Msg, remote string) *Session {
	client := hello.Client
	if client == "" {
		client = "unknown"
	}
	devID := normDeviceID(hello.Device)
	if u.ID != "" && hello.Token == "" {
		devID = "" // name + PIN logins are people, not beltpacks
	}
	s := &Session{
		Client:    client,
		Remote:    remote,
		DeviceID:  devID,
		devPerson: cleanPerson(hello.Name),
		Connected: time.Now(),
		hub:       h,
		out:       make(chan outFrame, 512),
		done:      make(chan struct{}),
		talk:      map[int]bool{},
		listen:    map[int]bool{},
		call:      map[int]bool{},
	}
	var dev *Device
	if s.DeviceID != "" && u.ID != "" {
		// Token login from a beltpack: remember it (Admit checked it may use this id).
		cfg, err := h.store.Update(func(c *Config) error {
			d := c.device(s.DeviceID)
			if d == nil {
				c.Devices = append(c.Devices, newDevice(s.DeviceID))
				d = &c.Devices[len(c.Devices)-1]
			}
			d.UserID = u.ID
			return nil
		})
		if err == nil {
			dev = cfg.device(s.DeviceID)
		}
	} else if s.DeviceID != "" {
		// New, unassigned beltpack: kept in memory only until an admin adopts it,
		// so anonymous connections can't fill the saved config.
		cfg := h.store.Get()
		if dev = cfg.device(s.DeviceID); dev == nil {
			d := newDevice(s.DeviceID)
			dev = &d
		}
		u.Name = dev.Name + " (unassigned)"
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

// personLocked is the name shown for the person behind this session.
func (s *Session) personLocked() string {
	if s.devPerson != "" {
		return s.devPerson
	}
	return s.user.Person
}

func settingsMsg(d *Device) proto.Settings {
	return proto.Settings{T: "settings", Name: d.Name, MicType: d.Audio.MicType, MicGain: d.Audio.MicGain,
		Sidetone: d.Audio.Sidetone, HPLevel: d.Audio.HPLevel, Bright: d.Audio.Bright}
}

func newDevice(id string) Device {
	return Device{ID: id, Name: defaultDeviceName(id), Net: NetConfig{Mode: "wifi", DHCP: true}, Audio: defaultDeviceAudio()}
}

func defaultDeviceName(id string) string {
	return "Beltpack " + strings.ToUpper(id[max(0, len(id)-4):])
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
	if u.Outputs {
		// The bridge's channel-outputs login hears every channel with an output.
		for _, ch := range cfg.Channels {
			if ch.Out != nil && len(s.slots) < 32 {
				s.slotIdx[ch.ID] = len(s.slots)
				s.slots = append(s.slots, slot{ch: ch, canListen: true})
				s.listen[ch.ID] = true
			}
		}
	}
	for _, a := range u.Channels {
		if u.Outputs {
			break
		}
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
		T:           "welcome",
		SID:         s.SID,
		Server:      cfg.ServerName,
		User:        s.user.Name,
		Person:      s.personLocked(),
		Admin:       s.user.Admin,
		Talk:        keys(s.talk),
		Listen:      keys(s.listen),
		Muted:       s.muted,
		TallyScreen: s.user.TallyScreen,
	}
	showName := map[int]string{}
	for _, sh := range cfg.Shows {
		showName[sh.ID] = sh.Name
	}
	spans := false // this user's channels come from more than one show
	for _, sl := range s.slots {
		spans = spans || sl.ch.Show != s.slots[0].ch.Show
	}
	for _, sl := range s.slots {
		ci := proto.ChannelInfo{
			ID: sl.ch.ID, Name: sl.ch.Name, Color: sl.ch.Color,
			CanTalk: sl.canTalk, CanListen: sl.canListen,
			ListenOnly: sl.ch.ListenOnly, Program: sl.ch.Program,
		}
		if len(cfg.Shows) > 1 {
			ci.Show = showName[sl.ch.Show]
		}
		if spans && s.Client == "beltpack" {
			// beltpacks show only the name: prefix the show so keys are unambiguous
			ci.Name = shortShow(showName[sl.ch.Show]) + " " + sl.ch.Name
		}
		if s.user.Outputs && sl.ch.Out != nil {
			ci.Out = &proto.ChannelOut{NDI: sl.ch.Out.NDI, Card: sl.ch.Out.Card}
			if ci.Show == "" {
				ci.Show = showName[sl.ch.Show]
			}
		}
		w.Channels = append(w.Channels, ci)
	}
	h.mu.RUnlock()
	if w.Channels == nil {
		w.Channels = []proto.ChannelInfo{}
	}
	s.sendJSON(w)
}

// shortShow abbreviates a show name for a beltpack key, e.g. "Main" -> "Main:",
// "Second Show" -> "SS:".
func shortShow(n string) string {
	f := strings.Fields(n)
	if len(f) > 1 {
		var b strings.Builder
		for _, w := range f {
			b.WriteString(strings.ToUpper(string([]rune(w)[:1])))
		}
		return b.String() + ":"
	}
	if r := []rune(n); len(r) > 5 {
		n = string(r[:5])
	}
	return n + ":"
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
	case "name":
		s.hub.mu.Lock()
		s.devPerson = cleanPerson(m.Name)
		s.hub.mu.Unlock()
		s.sendWelcome()
		s.hub.broadcastState()
	case "view":
		s.hub.mu.Lock()
		s.tallyView = m.On != nil && *m.On
		s.hub.mu.Unlock()
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
		t := proto.Talker{SID: s.SID, Name: s.user.Name, Person: s.personLocked()}
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
	Person    string           `json:"person,omitempty"`
	UserID    string           `json:"userId"`
	Show      int              `json:"show"`
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
	TallyView bool             `json:"tallyView"`
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
		SID: s.SID, User: s.user.Name, Person: s.personLocked(), UserID: s.user.ID, Show: s.user.Show, Client: s.Client, Remote: s.Remote,
		Device: s.DeviceID, Connected: s.Connected, Channels: chs,
		Talk: keys(s.talk), Listen: keys(s.listen), Call: keys(s.call), Muted: s.muted, Tally: s.tally, TallyView: s.tallyView,
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
	Op string `json:"op"` // talk | listen | call | mute | vol | tallyscreen | identify | reboot | kick
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
	case "tallyscreen":
		on := op.On
		s.sendJSON(proto.Msg{T: "remote", Op: "tallyscreen", On: &on})
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
	Pending   bool             `json:"pending,omitempty"` // connected, not adopted yet (not saved)
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
	for _, s := range h.sessions {
		if s.DeviceID != "" && s.user.ID == "" && cfg.device(s.DeviceID) == nil {
			info := s.infoLocked()
			out = append(out, DeviceInfo{Device: newDevice(s.DeviceID), Pending: true, Online: true, SID: s.SID, Remote: s.Remote,
				Telemetry: s.telemetry, Session: &info})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
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
	live := h.deviceSession(id)
	cfg, err := h.store.Update(func(c *Config) error {
		d := c.device(id)
		if d == nil && live != nil {
			// adopting a new beltpack that is connected right now
			c.Devices = append(c.Devices, newDevice(id))
			d = &c.Devices[len(c.Devices)-1]
		}
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
		if d.UserID == "" {
			return errors.New("assign a user to this beltpack first; network settings are only sent to an assigned beltpack")
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
	// Only a beltpack signed in with its assigned user's token gets the
	// settings (they include the Wi-Fi password).
	if s := h.deviceSession(id); s != nil && s.user.ID != "" && s.user.ID == d.UserID {
		s.sendJSON(proto.NetCfg{T: "netcfg", Mode: d.Net.Mode, DHCP: d.Net.DHCP, IP: d.Net.IP, Mask: d.Net.Mask,
			Gateway: d.Net.Gateway, DNS: d.Net.DNS, SSID: d.Net.SSID, Pass: d.Net.Pass, Server: d.Net.Server})
	}
	d.Net.Pass = ""
	return d, nil
}

func (h *Hub) DeleteDevice(id string) error {
	if s := h.deviceSession(id); s != nil && s.user.ID == "" {
		h.Kick(s.SID)
	}
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
