package bridge

import (
	"sync"
	"testing"
	"time"
)

// fakeStream plays like a card: each write takes 20 ms. It records the value
// of the first sample on each output channel per frame.
type fakeStream struct {
	ch     int
	mu     sync.Mutex
	frames [][]float32
	closed bool
}

func (f *fakeStream) Channels() int { return f.ch }
func (f *fakeStream) Write(b []float32) error {
	f.mu.Lock()
	f.frames = append(f.frames, append([]float32(nil), b[:f.ch]...))
	f.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	return nil
}
func (f *fakeStream) Close() { f.mu.Lock(); f.closed = true; f.mu.Unlock() }

func (f *fakeStream) played() (vals []float32, firstCh [][]float32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, fr := range f.frames {
		if fr[0] != 0 || fr[len(fr)-1] != 0 {
			vals = append(vals, fr[0])
			firstCh = append(firstCh, fr)
		}
	}
	return
}

func frame(v float32) []float32 {
	f := make([]float32, 960)
	for i := range f {
		f[i] = v
	}
	return f
}

func TestCardOutQueue(t *testing.T) {
	st := &fakeStream{ch: 3}
	o, err := newCardOut(func() (cardStream, error) { return st, nil }, []int{0, 1}, "fake", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Steady writes at the card's pace come out in order, on outputs 0 and 1 only.
	for v := 1; v <= 5; v++ {
		o.Write(frame(float32(v) / 10))
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(120 * time.Millisecond)
	vals, frs := st.played()
	if len(vals) != 5 {
		t.Fatalf("played %v, want 5 frames", vals)
	}
	for i, v := range vals {
		if want := float32(i+1) / 10; v != want || frs[i][1] != want || frs[i][2] != 0 {
			t.Fatalf("frame %d = %v, want %.1f on outputs 0 and 1, silence on 2", i, frs[i], want)
		}
	}
	// The card keeps playing (silence) when nothing arrives.
	st.mu.Lock()
	n := len(st.frames)
	st.mu.Unlock()
	if n < 8 {
		t.Fatalf("card should keep running with silence, only %d writes", n)
	}

	// A burst far faster than the card is trimmed, so latency stays bounded.
	for v := 1; v <= 40; v++ {
		o.Write(frame(1 + float32(v)/100))
	}
	time.Sleep(400 * time.Millisecond)
	vals, _ = st.played()
	burst := vals[5:]
	if len(burst) > cardQueueMax+1 || burst[len(burst)-1] != 1.4 {
		t.Fatalf("burst played %d frames ending %.2f; want at most %d ending with the newest", len(burst), burst[len(burst)-1], cardQueueMax+1)
	}
	o.Close()
	if !st.closed {
		t.Fatal("Close should close the card stream")
	}
}
