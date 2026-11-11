package opus

import (
	"math"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	enc, err := NewEncoder(AppVoIP)
	if err != nil {
		t.Skip("libopus not available:", err)
	}
	defer enc.Close()
	dec, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	pcm := make([]float32, Frame)
	var inE, outE float64
	for f := 0; f < 50; f++ {
		for i := range pcm {
			n := f*Frame + i
			pcm[i] = float32(0.3 * math.Sin(2*math.Pi*440*float64(n)/SampleRate))
		}
		pkt, err := enc.Encode(pcm)
		if err != nil || len(pkt) == 0 {
			t.Fatalf("encode: %v len=%d", err, len(pkt))
		}
		out, err := dec.Decode(pkt)
		if err != nil || len(out) != Frame {
			t.Fatalf("decode: %v len=%d", err, len(out))
		}
		if f > 10 {
			for i := range pcm {
				inE += float64(pcm[i] * pcm[i])
				outE += float64(out[i] * out[i])
			}
		}
	}
	ratio := outE / inE
	if ratio < 0.5 || ratio > 1.5 {
		t.Fatalf("energy ratio %.2f", ratio)
	}
	if plc, err := dec.Decode(nil); err != nil || len(plc) != Frame {
		t.Fatalf("plc: %v %d", err, len(plc))
	}
}
