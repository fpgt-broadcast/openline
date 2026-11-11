package bridge

import (
	"fmt"
	"math"
	"time"

	"openline/internal/ndi"
)

// DefaultOpener supports NDI (via the installed NDI runtime) and test tones.
var DefaultOpener = Opener{
	In: func(c *InConfig, extraIPs string) (AudioIn, error) {
		switch c.Type {
		case "", "ndi":
			return ndi.NewReceiver(c.Source, extraIPs, c.Channels)
		case "tone":
			return newTone(c.ToneHz), nil
		}
		return nil, fmt.Errorf("unknown input type %q", c.Type)
	},
	Out: func(c *OutConfig) (AudioOut, error) {
		switch c.Type {
		case "", "ndi":
			return ndi.NewSender(c.Name)
		case "null":
			return nullOut{}, nil
		}
		return nil, fmt.Errorf("unknown output type %q", c.Type)
	},
}

// tone is a test input: a beep, 1 s on / 1 s off, paced in real time.
type tone struct {
	hz    float64
	n     int
	start time.Time
	sent  int
}

func newTone(hz float64) *tone {
	if hz <= 0 {
		hz = 440
	}
	return &tone{hz: hz, start: time.Now()}
}

func (t *tone) Read(timeout time.Duration) ([]float32, int, error) {
	due := t.start.Add(time.Duration(t.sent+960) * time.Second / 48000)
	if d := time.Until(due); d > 0 {
		time.Sleep(d)
	}
	out := make([]float32, 960)
	for i := range out {
		if (t.n/48000)%2 == 0 {
			out[i] = float32(0.25 * math.Sin(2*math.Pi*t.hz*float64(t.n)/48000))
		}
		t.n++
	}
	t.sent += 960
	return out, 48000, nil
}

func (t *tone) Close() {}

type nullOut struct{}

func (nullOut) Write([]float32) {}
func (nullOut) Close()          {}
