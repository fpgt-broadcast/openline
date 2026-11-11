package bridge

import (
	"context"
	"io"
	"log"
	"math"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"openline/internal/client"
	"openline/internal/opus"
	"openline/internal/proto"
	"openline/internal/server"
)

type capOut struct {
	mu   sync.Mutex
	peak float32
	n    int
}

func (c *capOut) Write(b []float32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	for _, v := range b {
		c.peak = max(c.peak, abs32(v))
	}
}
func (c *capOut) Close() {}
func (c *capOut) get() (float32, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peak, c.n
}

func setup(t *testing.T) (*httptest.Server, *server.Store) {
	if err := opus.Load(); err != nil {
		t.Skip("libopus not available:", err)
	}
	store, _, err := server.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Update(func(c *server.Config) error {
		for i := range c.Users {
			if c.Users[i].Name == "Announcer Link" {
				c.Users[i].Token = "boothtoken0123456789"
			}
		}
		c.Users = append(c.Users,
			server.User{Name: "IFB Out", Bridge: true, Token: "ifbtoken012345678901", Channels: []server.Access{{Ch: 1, Listen: true}}},
		)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.New(store, fstest.MapFS{}).Handler())
	t.Cleanup(ts.Close)
	return ts, store
}

func quietLog() *log.Logger { return log.New(io.Discard, "", 0) }

func TestProgramToneReachesListeners(t *testing.T) {
	ts, store := setup(t)
	pgm := store.Get().Users[3] // "Program Feed" bridge user, talks on Program (5)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	link := NewLink(LinkConfig{Name: "pgm", Token: pgm.Token, Talk: []string{"Program"}, In: &InConfig{Type: "tone", Music: true}},
		ts.URL, "", DefaultOpener, quietLog())
	go link.Run(ctx)

	dir, err := client.Dial(ctx, ts.URL, proto.Msg{User: "Director", PIN: "1111"})
	if err != nil {
		t.Fatal(err)
	}
	pgmIdx := dir.ChannelIndex(5)
	var mu sync.Mutex
	got := 0
	dir.OnAudio = func(src, seq uint16, mask uint32, pkt []byte) {
		if mask&(1<<pgmIdx) != 0 {
			mu.Lock()
			got++
			mu.Unlock()
		}
	}
	go dir.Run(ctx)
	time.Sleep(1500 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if got < 40 { // ~70 frames expected in 1.5 s (always-on, no gate)
		t.Fatalf("director received %d program frames", got)
	}
}

func TestTalkerReachesNDIOutAndMixMinus(t *testing.T) {
	ts, _ := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ifb := &capOut{}
	booth := &capOut{}
	gate := -40.0
	op := Opener{
		In: DefaultOpener.In,
		Out: func(c *OutConfig) (AudioOut, error) {
			if c.Name == "ifb" {
				return ifb, nil
			}
			return booth, nil
		},
	}
	// IFB link: listens to Production, outputs to "NDI".
	go NewLink(LinkConfig{Name: "ifb", Token: "ifbtoken012345678901", Listen: []string{"Production"}, Out: &OutConfig{Name: "ifb"}}, ts.URL, "", op, quietLog()).Run(ctx)
	// Booth link: talks AND listens on Announcers with a gated tone input -> its own output must stay silent.
	go NewLink(LinkConfig{Name: "booth", Token: "boothtoken0123456789", Talk: []string{"Announcers"}, Listen: []string{"Announcers"},
		In: &InConfig{Type: "tone", GateDB: &gate}, Out: &OutConfig{Name: "booth"}}, ts.URL, "", op, quietLog()).Run(ctx)

	dir, err := client.Dial(ctx, ts.URL, proto.Msg{User: "Director", PIN: "1111"})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	announcerFrames := 0
	dir.OnAudio = func(src, seq uint16, mask uint32, pkt []byte) {
		if mask&(1<<dir.ChannelIndex(6)) != 0 {
			mu.Lock()
			announcerFrames++
			mu.Unlock()
		}
	}
	go dir.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	// Director talks on Production with a real Opus tone.
	dir.SetTalk(1, true)
	enc, _ := opus.NewEncoder(opus.AppVoIP)
	pcm := make([]float32, opus.Frame)
	for f := 0; f < 75; f++ {
		for i := range pcm {
			pcm[i] = float32(0.3 * math.Sin(2*math.Pi*300*float64(f*opus.Frame+i)/48000))
		}
		pkt, _ := enc.Encode(pcm)
		dir.SendAudio(uint16(f), pkt)
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)

	if p, n := ifb.get(); p < 0.1 || n < 50 {
		t.Fatalf("IFB output: peak %.3f over %d frames; expected director's voice", p, n)
	}
	if p, _ := booth.get(); p > 0.001 {
		t.Fatalf("booth output should not contain its own input (mix-minus); peak %.3f", p)
	}
	mu.Lock()
	defer mu.Unlock()
	if announcerFrames < 20 {
		t.Fatalf("director heard %d announcer frames; gate should have opened during tone", announcerFrames)
	}
}

func TestResampler(t *testing.T) {
	r := &resampler{}
	in := make([]float32, 441)
	total := 0
	for i := 0; i < 100; i++ { // 1 s of 44.1 kHz in 10 ms blocks
		total += len(r.process(in, 44100))
	}
	if total < 47990 || total > 48010 {
		t.Fatalf("resampled to %d samples, want ~48000", total)
	}
}
