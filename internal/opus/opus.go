// Package opus is a minimal libopus binding loaded at runtime via purego.
// Only 48 kHz mono float is used by OpenLine's bridge.
package opus

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"

	"github.com/ebitengine/purego"

	"openline/internal/dl"
)

const (
	AppVoIP  = 2048
	AppAudio = 2049

	SampleRate = 48000
	Frame      = 960 // 20 ms
)

var (
	loadOnce sync.Once
	loadErr  error

	encoderCreate  func(fs, ch, app int32, err *int32) uintptr
	encodeFloat    func(st uintptr, pcm *float32, frame int32, data *byte, max int32) int32
	encoderDestroy func(st uintptr)
	decoderCreate  func(fs, ch int32, err *int32) uintptr
	decodeFloat    func(st uintptr, data *byte, n int32, pcm *float32, frame int32, fec int32) int32
	decoderDestroy func(st uintptr)
)

func candidates() []string {
	if p := os.Getenv("OPUS_LIB"); p != "" {
		return []string{p}
	}
	switch runtime.GOOS {
	case "windows":
		return []string{"opus.dll", "libopus-0.dll", "libopus.dll"}
	case "darwin":
		return []string{"libopus.0.dylib", "/opt/homebrew/lib/libopus.0.dylib", "/usr/local/lib/libopus.0.dylib"}
	default:
		return []string{"libopus.so.0", "libopus.so"}
	}
}

// Load loads libopus. Safe to call repeatedly.
func Load() error {
	loadOnce.Do(func() {
		h, err := dl.Open(candidates()...)
		if err != nil {
			loadErr = fmt.Errorf("libopus not found (install it, or set OPUS_LIB): %w", err)
			return
		}
		purego.RegisterLibFunc(&encoderCreate, h, "opus_encoder_create")
		purego.RegisterLibFunc(&encodeFloat, h, "opus_encode_float")
		purego.RegisterLibFunc(&encoderDestroy, h, "opus_encoder_destroy")
		purego.RegisterLibFunc(&decoderCreate, h, "opus_decoder_create")
		purego.RegisterLibFunc(&decodeFloat, h, "opus_decode_float")
		purego.RegisterLibFunc(&decoderDestroy, h, "opus_decoder_destroy")
	})
	return loadErr
}

type Encoder struct {
	st  uintptr
	buf [1500]byte
}

// NewEncoder creates a 48 kHz mono encoder. app is AppVoIP or AppAudio.
func NewEncoder(app int) (*Encoder, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	var e int32
	st := encoderCreate(SampleRate, 1, int32(app), &e)
	if e != 0 || st == 0 {
		return nil, fmt.Errorf("opus_encoder_create: error %d", e)
	}
	return &Encoder{st: st}, nil
}

// Encode encodes exactly one 20 ms frame (960 samples). The returned slice
// is only valid until the next call.
func (e *Encoder) Encode(pcm []float32) ([]byte, error) {
	if len(pcm) != Frame {
		return nil, errors.New("opus: frame must be 960 samples")
	}
	n := encodeFloat(e.st, &pcm[0], Frame, &e.buf[0], int32(len(e.buf)))
	runtime.KeepAlive(pcm)
	if n < 0 {
		return nil, fmt.Errorf("opus_encode_float: error %d", n)
	}
	return e.buf[:n], nil
}

func (e *Encoder) Close() {
	if e.st != 0 {
		encoderDestroy(e.st)
		e.st = 0
	}
}

type Decoder struct {
	st  uintptr
	out [5760]float32
}

func NewDecoder() (*Decoder, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	var e int32
	st := decoderCreate(SampleRate, 1, &e)
	if e != 0 || st == 0 {
		return nil, fmt.Errorf("opus_decoder_create: error %d", e)
	}
	return &Decoder{st: st}, nil
}

// Decode returns 48 kHz mono samples (valid until the next call).
// A nil packet runs packet-loss concealment for one 20 ms frame.
func (d *Decoder) Decode(pkt []byte) ([]float32, error) {
	var p *byte
	if len(pkt) > 0 {
		p = &pkt[0]
	}
	frame := int32(len(d.out))
	if p == nil {
		frame = Frame
	}
	n := decodeFloat(d.st, p, int32(len(pkt)), &d.out[0], frame, 0)
	runtime.KeepAlive(pkt)
	if n < 0 {
		return nil, fmt.Errorf("opus_decode_float: error %d", n)
	}
	return d.out[:n], nil
}

func (d *Decoder) Close() {
	if d.st != 0 {
		decoderDestroy(d.st)
		d.st = 0
	}
}
