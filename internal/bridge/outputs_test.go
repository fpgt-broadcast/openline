package bridge

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"openline/internal/client"
	"openline/internal/opus"
	"openline/internal/proto"
	"openline/internal/server"
)

// fakeCard records per-channel peaks and paces like a real card (20 ms per write).
type fakeCard struct {
	mu    sync.Mutex
	n     int
	peaks []float32
	write int
}

func (c *fakeCard) Channels() int { return c.n }
func (c *fakeCard) Write(b []float32) error {
	c.mu.Lock()
	for i, v := range b {
		c.peaks[i%c.n] = max(c.peaks[i%c.n], abs32(v))
	}
	c.write++
	c.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	return nil
}
func (c *fakeCard) Close() {}
func (c *fakeCard) peak(ch int) float32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peaks[ch-1]
}

func sendTone(ctx context.Context, cl *client.Client, frames int) {
	enc, _ := opus.NewEncoder(opus.AppVoIP)
	defer enc.Close()
	pcm := make([]float32, opus.Frame)
	for f := 0; f < frames && ctx.Err() == nil; f++ {
		for i := range pcm {
			pcm[i] = float32(0.3 * math.Sin(2*math.Pi*300*float64(f*opus.Frame+i)/48000))
		}
		pkt, _ := enc.Encode(pcm)
		cl.SendAudio(uint16(f), pkt)
		time.Sleep(20 * time.Millisecond)
	}
}

// Two shows; Main Production goes to NDI + card output 1, Show B Production to
// card output 3. Each talker must land on exactly its own outputs.
func TestChannelOutputsNDIAndCard(t *testing.T) {
	ts, store := setup(t)
	_, err := store.Update(func(c *server.Config) error {
		c.Shows = append(c.Shows, server.Show{ID: 2, Name: "Show B"})
		c.Channels = append(c.Channels, server.Channel{ID: 11, Name: "Production", Show: 2, Out: &server.ChannelOut{Card: 3}})
		for i := range c.Channels {
			if c.Channels[i].ID == 1 {
				c.Channels[i].Out = &server.ChannelOut{NDI: true, Card: 1}
			}
		}
		c.Users = append(c.Users,
			server.User{Name: "Director", PIN: "9999", Show: 2, Channels: []server.Access{{Ch: 11, Talk: true, Listen: true}}},
			server.User{Name: "Venue Outputs", Outputs: true, Token: "outputstoken01234567"},
		)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	ndiOuts := map[string]*capOut{}
	op := Opener{In: DefaultOpener.In, Out: func(c *OutConfig) (AudioOut, error) {
		mu.Lock()
		defer mu.Unlock()
		o := &capOut{}
		ndiOuts[c.Name] = o
		return o, nil
	}}
	card := &fakeCard{n: 4, peaks: make([]float32, 4)}
	outs := NewOutputs(OutputsConfig{Token: "outputstoken01234567", Card: &CardConfig{}}, ts.URL, op, quietLog())
	outs.card = func(*CardConfig) (CardOut, string, error) { return card, "fake card", nil }
	go outs.Run(ctx)

	dirB, err := client.Dial(ctx, ts.URL, proto.Msg{User: "Director", PIN: "9999"})
	if err != nil {
		t.Fatal(err)
	}
	go dirB.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	// Only Show B's director talks: card output 3 only; NDI and card 1 stay silent.
	dirB.SetTalk(11, true)
	sendTone(ctx, dirB, 60)
	time.Sleep(300 * time.Millisecond)
	if p := card.peak(3); p < 0.1 {
		t.Fatalf("card output 3 (Show B Production) peak %.3f; expected the tone", p)
	}
	if p := card.peak(1); p > 0.001 {
		t.Fatalf("card output 1 (Main Production) should be silent, peak %.3f", p)
	}
	mu.Lock()
	main := ndiOuts["OpenLine Main Production"]
	mu.Unlock()
	if main == nil {
		t.Fatalf("no NDI source for Main Production; got %v", ndiOuts)
	}
	if p, n := main.get(); p > 0.001 || n < 20 {
		t.Fatalf("NDI Main Production: peak %.3f over %d frames; expected silence but running", p, n)
	}
	if _, ok := ndiOuts["OpenLine Show B Production"]; ok {
		t.Fatal("Show B Production has no NDI output configured")
	}

	// Main director talks: NDI + card output 1.
	dirA, err := client.Dial(ctx, ts.URL, proto.Msg{User: "Director", PIN: "1111"})
	if err != nil {
		t.Fatal(err)
	}
	go dirA.Run(ctx)
	time.Sleep(200 * time.Millisecond)
	dirA.SetTalk(1, true)
	sendTone(ctx, dirA, 60)
	time.Sleep(300 * time.Millisecond)
	if p := card.peak(1); p < 0.1 {
		t.Fatalf("card output 1 peak %.3f; expected Main Production", p)
	}
	if p, _ := main.get(); p < 0.1 {
		t.Fatalf("NDI Main Production peak %.3f; expected the tone", p)
	}
	if p := card.peak(2) + card.peak(4); p > 0.001 {
		t.Fatalf("unused card outputs should be silent, peak %.3f", p)
	}

}
