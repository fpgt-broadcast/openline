// Package bridge connects NDI audio to OpenLine channels.
//
// Each Link is ONE intercom session that can both talk (an NDI input, e.g.
// the announcers' talkback mic) and listen (a mix of channels sent out as a
// new NDI source, e.g. an IFB feed back to the announcers). Because input and
// output share one session, the server's mix-minus keeps the announcers from
// hearing their own voice come back delayed.
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"openline/internal/client"
	"openline/internal/opus"
	"openline/internal/proto"
)

type Config struct {
	Server   string       `json:"server"`      // https://intercom.example.com
	ExtraIPs string       `json:"ndiExtraIPs"` // optional NDI discovery IPs for routed networks
	Links    []LinkConfig `json:"links"`
}

type LinkConfig struct {
	Name   string     `json:"name"`
	Token  string     `json:"token"`  // token of an OpenLine user with the "NDI bridge" role
	Talk   []string   `json:"talk"`   // channels the input is sent to
	Listen []string   `json:"listen"` // channels mixed into the output
	In     *InConfig  `json:"in,omitempty"`
	Out    *OutConfig `json:"out,omitempty"`
}

type InConfig struct {
	Type     string   `json:"type"`     // ndi | tone
	Source   string   `json:"source"`   // NDI source name, e.g. "BOOTH-PC (Talkback)"
	Channels []int    `json:"channels"` // NDI audio channels to use (0-based), empty = all mixed to mono
	GainDB   float64  `json:"gainDb"`
	GateDB   *float64 `json:"gateDb"` // talk only when louder than this (dBFS). null/omitted = always on (program feeds)
	HoldMs   int      `json:"holdMs"` // gate hold time, default 600
	Music    bool     `json:"music"`  // encode for music/program rather than speech
	ToneHz   float64  `json:"toneHz"` // type=tone: test tone, 1 s on / 1 s off
}

type OutConfig struct {
	Type   string  `json:"type"` // ndi | null
	Name   string  `json:"name"` // NDI source name to publish, e.g. "OpenLine IFB Announcers"
	GainDB float64 `json:"gainDb"`
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.Server == "" {
		return nil, errors.New("config: server is required")
	}
	for i, l := range c.Links {
		if l.Token == "" {
			return nil, fmt.Errorf("link %d (%s): token is required", i, l.Name)
		}
		if l.In == nil && l.Out == nil {
			return nil, fmt.Errorf("link %d (%s): needs in and/or out", i, l.Name)
		}
		if l.In != nil && len(l.Talk) == 0 {
			return nil, fmt.Errorf("link %d (%s): input needs talk channels", i, l.Name)
		}
		if l.Out != nil && len(l.Listen) == 0 {
			return nil, fmt.Errorf("link %d (%s): output needs listen channels", i, l.Name)
		}
		if c.Links[i].Name == "" {
			c.Links[i].Name = fmt.Sprintf("link%d", i+1)
		}
	}
	return &c, nil
}

// AudioIn produces 48 kHz-or-other mono blocks of any size.
type AudioIn interface {
	Read(timeout time.Duration) (samples []float32, rate int, err error)
	Close()
}

// AudioOut consumes 20 ms 48 kHz mono blocks.
type AudioOut interface {
	Write(mono []float32)
	Close()
}

// Opener builds inputs/outputs; replaced in tests and by the NDI build.
type Opener struct {
	In  func(cfg *InConfig, extraIPs string) (AudioIn, error)
	Out func(cfg *OutConfig) (AudioOut, error)
}

// Link runs one bridge link until ctx is done.
type Link struct {
	cfg    LinkConfig
	server string
	extra  string
	open   Opener
	log    *log.Logger

	mu       sync.Mutex
	cl       *client.Client
	talkIDs  []int
	talkOn   bool
	seq      uint16
	mixer    *mixer
	stats    linkStats
	lastWarn time.Time
}

type linkStats struct {
	connected  bool
	inPeak     float32
	outPeak    float32
	gateOpen   bool
	framesSent int
}

func NewLink(cfg LinkConfig, server, extraIPs string, op Opener, lg *log.Logger) *Link {
	return &Link{cfg: cfg, server: server, extra: extraIPs, open: op, log: lg, mixer: newMixer()}
}

func (l *Link) logf(f string, a ...any) { l.log.Printf("[%s] "+f, append([]any{l.cfg.Name}, a...)...) }

func (l *Link) Run(ctx context.Context) {
	var wg sync.WaitGroup
	if l.cfg.In != nil {
		wg.Add(1)
		go func() { defer wg.Done(); l.inputLoop(ctx) }()
	}
	if l.cfg.Out != nil {
		wg.Add(1)
		go func() { defer wg.Done(); l.outputLoop(ctx) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); l.connLoop(ctx) }()
	wg.Add(1)
	go func() { defer wg.Done(); l.statusLoop(ctx) }()
	wg.Wait()
}

// ---------------- server connection ----------------

func (l *Link) connLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		cl, err := client.Dial(ctx, l.server, proto.Msg{Token: l.cfg.Token, Client: "bridge"})
		if err != nil {
			l.logf("connect: %v (retry in %s)", err, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff = min(backoff*2, 15*time.Second)
			continue
		}
		backoff = time.Second
		cl.OnAudio = func(src, seq uint16, mask uint32, pkt []byte) { l.mixer.push(src, pkt) }
		cl.OnWelcome = func(w proto.Welcome) { l.applyWelcome(cl, w) }
		cl.OnControl = func(t string, raw []byte) {
			if t == "error" {
				var m proto.Msg
				json.Unmarshal(raw, &m)
				l.logf("server: %s", m.Msg)
			}
		}
		l.applyWelcome(cl, cl.Welcome())
		l.logf("connected as %q", cl.Welcome().User)
		rctx, cancel := context.WithCancel(ctx)
		go func() { <-rctx.Done(); cl.Close() }()
		err = cl.Run(rctx)
		cancel()
		l.mu.Lock()
		l.cl = nil
		l.talkOn = false
		l.stats.connected = false
		l.mu.Unlock()
		if ctx.Err() == nil {
			l.logf("disconnected: %v", err)
			time.Sleep(time.Second)
		}
	}
}

func (l *Link) applyWelcome(cl *client.Client, w proto.Welcome) {
	resolve := func(names []string) (ids []int) {
		for _, n := range names {
			id, err := cl.ChannelID(n)
			if err != nil {
				l.logf("WARNING: %v", err)
				continue
			}
			ids = append(ids, id)
		}
		return
	}
	talk := resolve(l.cfg.Talk)
	want := map[int]bool{}
	for _, id := range resolve(l.cfg.Listen) {
		want[id] = true
	}
	listening := map[int]bool{}
	for _, id := range w.Listen {
		listening[id] = true
	}
	for _, ch := range w.Channels {
		if want[ch.ID] != listening[ch.ID] {
			cl.SetListen(ch.ID, want[ch.ID])
		}
	}
	for _, id := range talk {
		for _, ch := range w.Channels {
			if ch.ID == id && !ch.CanTalk {
				l.logf("WARNING: user %q may not talk on %q (give it Talk access; listen-only channels need the NDI bridge role)", w.User, ch.Name)
			}
		}
	}
	l.mu.Lock()
	l.cl = cl
	l.talkIDs = talk
	l.talkOn = false
	l.stats.connected = true
	l.mu.Unlock()
}

func (l *Link) setTalk(on bool) {
	// caller holds l.mu
	if l.cl == nil || l.talkOn == on {
		return
	}
	for _, id := range l.talkIDs {
		l.cl.SetTalk(id, on)
	}
	l.talkOn = on
}

// ---------------- input: NDI -> Opus -> server ----------------

func (l *Link) inputLoop(ctx context.Context) {
	app := opus.AppVoIP
	if l.cfg.In.Music {
		app = opus.AppAudio
	}
	enc, err := opus.NewEncoder(app)
	if err != nil {
		l.logf("input disabled: %v", err)
		return
	}
	defer enc.Close()
	gain := float32(math.Pow(10, l.cfg.In.GainDB/20))
	hold := time.Duration(l.cfg.In.HoldMs) * time.Millisecond
	if hold == 0 {
		hold = 600 * time.Millisecond
	}
	var gateLin float32 = -1
	if l.cfg.In.GateDB != nil {
		gateLin = float32(math.Pow(10, *l.cfg.In.GateDB/20))
	}
	for ctx.Err() == nil {
		in, err := l.open.In(l.cfg.In, l.extra)
		if err != nil {
			l.logf("input: %v (retry in 5s)", err)
			select {
			case <-time.After(5 * time.Second):
				continue
			case <-ctx.Done():
				return
			}
		}
		l.logf("input open")
		rs := &resampler{}
		frame := make([]float32, 0, opus.Frame*2)
		var lastLoud time.Time
		for ctx.Err() == nil {
			blk, rate, err := in.Read(500 * time.Millisecond)
			if err != nil {
				l.logf("input: %v", err)
				break
			}
			if blk == nil {
				continue
			}
			for _, v := range rs.process(blk, rate) {
				frame = append(frame, v*gain)
				if len(frame) < opus.Frame {
					continue
				}
				var peak float32
				for _, s := range frame {
					peak = max(peak, abs32(s))
				}
				if peak >= gateLin {
					lastLoud = time.Now()
				}
				open := gateLin < 0 || time.Since(lastLoud) < hold
				pkt, err := enc.Encode(frame)
				frame = frame[:0]
				l.mu.Lock()
				l.stats.inPeak = max(l.stats.inPeak, peak)
				l.stats.gateOpen = open
				l.setTalk(open)
				cl := l.cl
				seq := l.seq
				l.seq++
				if open && cl != nil && err == nil {
					l.stats.framesSent++
				}
				l.mu.Unlock()
				if open && cl != nil && err == nil {
					cl.SendAudio(seq, pkt)
				}
			}
		}
		in.Close()
		l.mu.Lock()
		l.setTalk(false)
		l.mu.Unlock()
	}
}

// ---------------- output: server -> mix -> NDI ----------------

func (l *Link) outputLoop(ctx context.Context) {
	out, err := l.open.Out(l.cfg.Out)
	if err != nil {
		l.logf("output disabled: %v", err)
		return
	}
	defer out.Close()
	l.logf("output open")
	gain := float32(math.Pow(10, l.cfg.Out.GainDB/20))
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	buf := make([]float32, opus.Frame)
	next := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		// Catch up if the ticker slipped (keeps the output clock honest).
		for !next.After(time.Now()) {
			l.mixer.pull(buf)
			var peak float32
			for i := range buf {
				buf[i] *= gain
				peak = max(peak, abs32(buf[i]))
			}
			out.Write(buf)
			l.mu.Lock()
			l.stats.outPeak = max(l.stats.outPeak, peak)
			l.mu.Unlock()
			next = next.Add(20 * time.Millisecond)
		}
	}
}

func (l *Link) statusLoop(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		l.mu.Lock()
		s := l.stats
		l.stats.inPeak, l.stats.outPeak, l.stats.framesSent = 0, 0, 0
		l.mu.Unlock()
		var parts []string
		parts = append(parts, map[bool]string{true: "online", false: "OFFLINE"}[s.connected])
		if l.cfg.In != nil {
			parts = append(parts, fmt.Sprintf("in peak %s, sent %.1fs", dbfs(s.inPeak), float64(s.framesSent)*0.02))
		}
		if l.cfg.Out != nil {
			parts = append(parts, fmt.Sprintf("out peak %s", dbfs(s.outPeak)))
		}
		l.logf("%s", strings.Join(parts, " · "))
	}
}

func dbfs(p float32) string {
	if p <= 0 {
		return "-inf dB"
	}
	return fmt.Sprintf("%.0f dB", 20*math.Log10(float64(p)))
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// ---------------- mixer with per-talker jitter buffers ----------------

type mixSrc struct {
	dec    *opus.Decoder
	q      [][]float32
	primed bool
	last   time.Time
}

type mixer struct {
	mu   sync.Mutex
	srcs map[uint16]*mixSrc
}

func newMixer() *mixer { return &mixer{srcs: map[uint16]*mixSrc{}} }

const (
	jitterFrames = 3  // 60 ms before a talker starts playing
	maxFrames    = 12 // drop beyond 240 ms queued
)

func (m *mixer) push(sid uint16, pkt []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.srcs[sid]
	if s == nil {
		dec, err := opus.NewDecoder()
		if err != nil {
			return
		}
		s = &mixSrc{dec: dec}
		m.srcs[sid] = s
	}
	pcm, err := s.dec.Decode(pkt)
	if err != nil {
		return
	}
	s.q = append(s.q, append([]float32(nil), pcm...))
	s.last = time.Now()
	for len(s.q) > maxFrames {
		s.q = s.q[1:]
	}
}

// pull mixes one 20 ms frame into buf.
func (m *mixer) pull(buf []float32) {
	clear(buf)
	m.mu.Lock()
	defer m.mu.Unlock()
	for sid, s := range m.srcs {
		if !s.primed {
			if len(s.q) >= jitterFrames {
				s.primed = true
			} else {
				if time.Since(s.last) > 10*time.Second {
					s.dec.Close()
					delete(m.srcs, sid)
				}
				continue
			}
		}
		if len(s.q) == 0 {
			s.primed = false
			continue
		}
		f := s.q[0]
		s.q = s.q[1:]
		for i := 0; i < len(buf) && i < len(f); i++ {
			buf[i] += f[i]
		}
	}
	for i, v := range buf {
		if v > 1 || v < -1 {
			buf[i] = float32(math.Tanh(float64(v)))
		}
	}
}

// ---------------- linear resampler to 48 kHz ----------------

type resampler struct {
	pos  float64
	prev float32
	rate int
}

func (r *resampler) process(in []float32, rate int) []float32 {
	if rate == 48000 || rate <= 0 {
		return in
	}
	if rate != r.rate {
		r.rate, r.pos, r.prev = rate, 0, 0
	}
	step := float64(rate) / 48000
	out := make([]float32, 0, int(float64(len(in))/step)+2)
	// pos indexes into [prev, in...] where index 0 = prev
	for {
		i := int(r.pos)
		if i+1 > len(in) {
			break
		}
		var a float32
		if i == 0 {
			a = r.prev
		} else {
			a = in[i-1]
		}
		b := in[i]
		frac := float32(r.pos - float64(i))
		out = append(out, a+(b-a)*frac)
		r.pos += step
	}
	r.pos -= float64(len(in))
	r.prev = in[len(in)-1]
	return out
}
