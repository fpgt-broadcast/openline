package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"openline/internal/client"
	"openline/internal/opus"
	"openline/internal/portaudio"
	"openline/internal/proto"
)

// OutputsConfig plays intercom channels out at the venue. Which channels go
// where is chosen in Admin → Channels (Output column); this login just needs
// the token of a user with the "Channel outputs" role.
type OutputsConfig struct {
	Token     string      `json:"token"`
	NDIPrefix string      `json:"ndiPrefix"` // NDI source name prefix, default "OpenLine"
	GainDB    float64     `json:"gainDb"`
	Card      *CardConfig `json:"card,omitempty"` // needed only if a channel uses a sound card output
}

// CardConfig selects the sound card for "card" outputs.
type CardConfig struct {
	Device    string `json:"device"`    // name or part of it (openline-bridge -devices); "" = system default
	LatencyMs int    `json:"latencyMs"` // output buffer, default 60
}

// CardOut is an open multichannel sound card output. Write blocks until the
// card has room, so it paces the output loop at the card's own clock.
type CardOut interface {
	Channels() int
	Write(interleaved []float32) error
	Close()
}

// OpenCard opens the configured sound card through PortAudio.
func OpenCard(c *CardConfig) (CardOut, string, error) {
	dev, err := portaudio.FindOutput(c.Device)
	if err != nil {
		return nil, "", err
	}
	lat := time.Duration(c.LatencyMs) * time.Millisecond
	if lat <= 0 {
		lat = 60 * time.Millisecond
	}
	st, err := portaudio.OpenOutput(dev, min(dev.MaxOut, 64), lat)
	if err != nil {
		return nil, "", err
	}
	return st, fmt.Sprintf("%s (%s, %d outputs)", dev.Name, dev.HostAPI, st.Channels()), nil
}

type outChan struct {
	id        int
	name      string // NDI source name ("" = no NDI output)
	card      int    // 1-based card output, 0 = none
	label     string // for logs
	mix       *mixer
	ndi       AudioOut
	ndiName   string // name the open NDI sender was created with
	ndiFailed time.Time
	peak      float32
	buf       []float32
}

// Outputs runs the channel-outputs login until ctx is done.
type Outputs struct {
	cfg    OutputsConfig
	server string
	open   Opener
	card   func(*CardConfig) (CardOut, string, error)
	log    *log.Logger

	mu        sync.Mutex
	chans     map[int]*outChan
	bits      []int // mask bit -> channel id
	connected bool
	user      string
}

func NewOutputs(cfg OutputsConfig, server string, op Opener, lg *log.Logger) *Outputs {
	if cfg.NDIPrefix == "" {
		cfg.NDIPrefix = "OpenLine"
	}
	return &Outputs{cfg: cfg, server: server, open: op, card: OpenCard, log: lg, chans: map[int]*outChan{}}
}

func (o *Outputs) logf(f string, a ...any) { o.log.Printf("[outputs] "+f, a...) }

func (o *Outputs) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); o.connLoop(ctx) }()
	go func() { defer wg.Done(); o.playLoop(ctx) }()
	go func() { defer wg.Done(); o.statusLoop(ctx) }()
	wg.Wait()
	o.mu.Lock()
	for _, c := range o.chans {
		if c.ndi != nil {
			c.ndi.Close()
		}
	}
	o.mu.Unlock()
}

func (o *Outputs) connLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		cl, err := client.Dial(ctx, o.server, proto.Msg{Token: o.cfg.Token, Client: "bridge"})
		if err != nil {
			o.logf("connect: %v (retry in %s)", err, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff = min(backoff*2, 15*time.Second)
			continue
		}
		backoff = time.Second
		cl.OnAudio = o.onAudio
		cl.OnWelcome = o.apply
		cl.OnControl = func(t string, raw []byte) {
			if t == "error" {
				var m proto.Msg
				json.Unmarshal(raw, &m)
				o.logf("server: %s", m.Msg)
			}
		}
		o.apply(cl.Welcome())
		rctx, cancel := context.WithCancel(ctx)
		go func() { <-rctx.Done(); cl.Close() }()
		err = cl.Run(rctx)
		cancel()
		o.mu.Lock()
		o.connected = false
		o.mu.Unlock()
		if ctx.Err() == nil {
			o.logf("disconnected: %v", err)
			time.Sleep(time.Second)
		}
	}
}

func (o *Outputs) onAudio(src, seq uint16, mask uint32, pkt []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for bit := 0; bit < len(o.bits) && mask != 0; bit++ {
		if mask&(1<<uint(bit)) == 0 {
			continue
		}
		mask &^= 1 << uint(bit)
		if c := o.chans[o.bits[bit]]; c != nil {
			c.mix.push(src, pkt)
		}
	}
}

// apply rebuilds the output list from a welcome (sent again on every config change).
func (o *Outputs) apply(w proto.Welcome) {
	shows := map[string]bool{}
	for _, ch := range w.Channels {
		shows[ch.Show] = true
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.connected, o.user = true, w.User
	o.bits = o.bits[:0]
	keep := map[int]bool{}
	var lines []string
	for _, ch := range w.Channels {
		o.bits = append(o.bits, ch.ID)
		if ch.Out == nil {
			continue
		}
		keep[ch.ID] = true
		c := o.chans[ch.ID]
		if c == nil {
			c = &outChan{id: ch.ID, mix: newMixer(), buf: make([]float32, opus.Frame)}
			o.chans[ch.ID] = c
		}
		label := ch.Name
		if len(shows) > 1 && ch.Show != "" {
			label = ch.Show + " " + ch.Name
		}
		c.label, c.card, c.name = label, ch.Out.Card, ""
		var dests []string
		if ch.Out.NDI {
			c.name = o.cfg.NDIPrefix + " " + label
			dests = append(dests, fmt.Sprintf("NDI %q", c.name))
		}
		if c.card > 0 {
			dests = append(dests, fmt.Sprintf("sound card output %d", c.card))
		}
		lines = append(lines, label+" → "+strings.Join(dests, " + "))
	}
	for id, c := range o.chans {
		if !keep[id] {
			if c.ndi != nil {
				c.ndi.Close()
			}
			delete(o.chans, id)
		}
	}
	if len(lines) == 0 {
		o.logf("connected as %q, but no channel has an output yet: set one in Admin → Channels → Output (and give this user the \"Channel outputs\" role)", w.User)
		return
	}
	sort.Strings(lines)
	o.logf("connected as %q: %s", w.User, strings.Join(lines, " · "))
	if o.cfg.Card == nil {
		for _, c := range o.chans {
			if c.card > 0 {
				o.logf("WARNING: %s uses a sound card output but bridge.json has no \"card\" section in \"outputs\"", c.label)
			}
		}
	}
}

// playLoop pulls 20 ms from every channel and sends it to NDI and the card.
// With a card open, its blocking write is the clock; otherwise a ticker is.
func (o *Outputs) playLoop(ctx context.Context) {
	gain := float32(math.Pow(10, o.cfg.GainDB/20))
	var card CardOut
	var cardBuf []float32
	var cardRetry time.Time
	next := time.Now()
	defer func() {
		if card != nil {
			card.Close()
		}
	}()
	for ctx.Err() == nil {
		if card == nil && o.cfg.Card != nil && time.Now().After(cardRetry) && o.needsCard() {
			c, desc, err := o.card(o.cfg.Card)
			if err != nil {
				o.logf("sound card: %v (retry in 10s)", err)
				cardRetry = time.Now().Add(10 * time.Second)
			} else {
				card, cardBuf = c, make([]float32, opus.Frame*c.Channels())
				o.logf("sound card open: %s", desc)
				o.warnCardRange(card.Channels())
			}
		}
		if card == nil {
			// No card: pace with the wall clock.
			if d := time.Until(next); d > 0 {
				select {
				case <-time.After(d):
				case <-ctx.Done():
					return
				}
			}
			next = next.Add(20 * time.Millisecond)
			if time.Since(next) > 200*time.Millisecond {
				next = time.Now() // fell far behind (sleep/suspend): don't burst
			}
		} else {
			clear(cardBuf)
		}
		o.mu.Lock()
		for _, c := range o.chans {
			c.mix.pull(c.buf)
			for i := range c.buf {
				c.buf[i] *= gain
				c.peak = max(c.peak, abs32(c.buf[i]))
			}
			o.sendNDILocked(c)
			if card != nil && c.card > 0 && c.card <= card.Channels() {
				n := card.Channels()
				for i, v := range c.buf {
					cardBuf[i*n+c.card-1] += v
				}
			}
		}
		o.mu.Unlock()
		if card != nil {
			if err := card.Write(cardBuf); err != nil {
				o.logf("sound card: %v (reopening)", err)
				card.Close()
				card = nil
				next = time.Now()
				cardRetry = time.Now().Add(2 * time.Second)
				continue
			}
			// A real card blocks in Write and is the clock. Some virtual devices
			// accept data instantly: never run more than 1 s ahead of real time.
			next = next.Add(20 * time.Millisecond)
			if ahead := time.Until(next); ahead > time.Second {
				time.Sleep(ahead - time.Second)
			} else if ahead < -200*time.Millisecond {
				next = time.Now()
			}
		}
	}
}

func (o *Outputs) needsCard() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, c := range o.chans {
		if c.card > 0 {
			return true
		}
	}
	return false
}

func (o *Outputs) warnCardRange(n int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, c := range o.chans {
		if c.card > n {
			o.logf("WARNING: %s is set to sound card output %d, but the card has only %d", c.label, c.card, n)
		}
	}
}

// sendNDILocked (re)creates the channel's NDI source when needed and sends the frame.
func (o *Outputs) sendNDILocked(c *outChan) {
	if c.ndi != nil && c.ndiName != c.name { // renamed, or NDI output turned off
		c.ndi.Close()
		c.ndi, c.ndiName = nil, ""
	}
	if c.name == "" {
		return
	}
	if c.ndi == nil {
		if time.Since(c.ndiFailed) < 10*time.Second {
			return
		}
		out, err := o.open.Out(&OutConfig{Type: "ndi", Name: c.name})
		if err != nil {
			o.logf("NDI output %q: %v (retry in 10s)", c.name, err)
			c.ndiFailed = time.Now()
			return
		}
		c.ndi, c.ndiName = out, c.name
		o.logf("NDI source %q is live", c.name)
	}
	c.ndi.Write(c.buf)
}

func (o *Outputs) statusLoop(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		o.mu.Lock()
		state := map[bool]string{true: "online", false: "OFFLINE"}[o.connected]
		var parts []string
		for _, c := range o.chans {
			parts = append(parts, fmt.Sprintf("%s %s", c.label, dbfs(c.peak)))
			c.peak = 0
		}
		o.mu.Unlock()
		sort.Strings(parts)
		o.logf("%s · %s", state, strings.Join(parts, " · "))
	}
}
