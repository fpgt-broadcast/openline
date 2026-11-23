package bridge

import (
	"fmt"
	"log"
	"sync"
	"time"

	"openline/internal/portaudio"
)

// Sound card links: a link's "in" and/or "out" can be a sound card instead of
// NDI, e.g. the Saramonic WiTalk9 link port through a USB headset adapter, a
// wired party-line interface, a walkie-talkie or a mixer.

// cardIn reads 20 ms blocks from a sound card input and mixes the chosen
// input channels to mono. The card's clock paces it.
type cardIn struct {
	st    *portaudio.InStream
	picks []int
	buf   []float32
	start time.Time
	read  int // frames so far
}

func openCardIn(c *InConfig) (AudioIn, error) {
	dev, err := portaudio.FindInput(c.Device)
	if err != nil {
		return nil, err
	}
	picks := c.Channels
	if len(picks) == 0 {
		picks = []int{0}
	}
	need := 0
	for _, p := range picks {
		if p < 0 {
			return nil, fmt.Errorf("input channel %d: channels count from 0", p)
		}
		need = max(need, p+1)
	}
	st, err := portaudio.OpenInput(dev, need, 40*time.Millisecond)
	if err != nil {
		return nil, err
	}
	return &cardIn{st: st, picks: picks, buf: make([]float32, st.Rate/50*st.Channels()), start: time.Now()}, nil
}

func (c *cardIn) Read(time.Duration) ([]float32, int, error) {
	if err := c.st.Read(c.buf); err != nil {
		return nil, 0, fmt.Errorf("sound card %q: %w", c.st.Device.Name, err)
	}
	n := c.st.Channels()
	// A real card blocks in Read and is the clock. Some virtual devices hand
	// data over instantly: never run more than 1 s ahead of real time.
	c.read += len(c.buf) / n
	if ahead := time.Duration(c.read)*time.Second/time.Duration(c.st.Rate) - time.Since(c.start); ahead > time.Second {
		time.Sleep(ahead - time.Second)
	} else if ahead < -2*time.Second {
		c.start, c.read = time.Now(), 0 // fell behind (suspend etc.): restart the count
	}
	out := make([]float32, len(c.buf)/n)
	scale := 1 / float32(len(c.picks))
	for i := range out {
		var v float32
		for _, p := range c.picks {
			v += c.buf[i*n+p]
		}
		out[i] = v * scale
	}
	return out, c.st.Rate, nil
}

func (c *cardIn) Close() { c.st.Close() }

// cardStream is the part of portaudio.Stream cardOut uses (faked in tests).
type cardStream interface {
	Channels() int
	Write(interleaved []float32) error
	Close()
}

// cardOut plays the link's 20 ms mono blocks on chosen sound card outputs.
// The link writes on the computer's clock and the card plays on its own, so a
// short queue sits between them: when the card runs a little faster it plays
// a frame of silence, when it runs slower the queue drops a frame. Either
// happens at most every few minutes.
type cardOut struct {
	open  func() (cardStream, error)
	picks []int
	log   *log.Logger
	name  string

	mu     sync.Mutex
	q      [][]float32
	closed bool
	done   chan struct{}
}

const (
	cardQueueMax    = 8 // 160 ms: drop down to cardQueueTarget beyond this
	cardQueueTarget = 3
)

func openCardOut(c *OutConfig, lg *log.Logger) (AudioOut, error) {
	dev, err := portaudio.FindOutput(c.Device)
	if err != nil {
		return nil, err
	}
	picks := c.Channels
	if len(picks) == 0 {
		picks = []int{0}
		if dev.MaxOut >= 2 {
			picks = []int{0, 1} // a headset adapter: both ears
		}
	}
	need := 0
	for _, p := range picks {
		if p < 0 {
			return nil, fmt.Errorf("output channel %d: channels count from 0", p)
		}
		need = max(need, p+1)
	}
	if need > dev.MaxOut {
		return nil, fmt.Errorf("%q has %d outputs, %d needed", dev.Name, dev.MaxOut, need)
	}
	open := func() (cardStream, error) { return portaudio.OpenOutput(dev, need, 60*time.Millisecond) }
	return newCardOut(open, picks, dev.Name, lg)
}

func newCardOut(open func() (cardStream, error), picks []int, name string, lg *log.Logger) (*cardOut, error) {
	st, err := open()
	if err != nil {
		return nil, err
	}
	o := &cardOut{open: open, picks: picks, log: lg, name: name, done: make(chan struct{})}
	go o.play(st)
	return o, nil
}

func (o *cardOut) Write(mono []float32) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return
	}
	o.q = append(o.q, append([]float32(nil), mono...))
	if len(o.q) > cardQueueMax {
		o.q = o.q[len(o.q)-cardQueueTarget:]
	}
}

func (o *cardOut) next() ([]float32, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil, false
	}
	if len(o.q) == 0 {
		return nil, true
	}
	f := o.q[0]
	o.q = o.q[1:]
	return f, true
}

func (o *cardOut) play(st cardStream) {
	defer close(o.done)
	buf := []float32(nil)
	start, written := time.Now(), 0 // frames, for the real-time guard
	for {
		f, ok := o.next()
		if !ok {
			st.Close()
			return
		}
		n := st.Channels()
		frames := 960
		if f != nil {
			frames = len(f)
		}
		if cap(buf) < frames*n {
			buf = make([]float32, frames*n)
		}
		buf = buf[:frames*n]
		clear(buf)
		for i, v := range f {
			for _, p := range o.picks {
				buf[i*n+p] = v
			}
		}
		// A real card blocks in Write and is the clock. Some virtual devices
		// accept data instantly: never run more than 1 s ahead of real time.
		written += frames
		if ahead := time.Duration(written)*time.Second/48000 - time.Since(start); ahead > time.Second {
			time.Sleep(ahead - time.Second)
		} else if ahead < -2*time.Second {
			start, written = time.Now(), 0
		}
		if err := st.Write(buf); err != nil {
			if o.log != nil {
				o.log.Printf("sound card %q: %v (reopening)", o.name, err)
			}
			st.Close()
			for {
				time.Sleep(2 * time.Second)
				if _, ok := o.next(); !ok {
					return
				}
				if st, err = o.open(); err == nil {
					break
				}
			}
		}
	}
}

func (o *cardOut) Close() {
	o.mu.Lock()
	o.closed = true
	o.mu.Unlock()
	<-o.done
}
