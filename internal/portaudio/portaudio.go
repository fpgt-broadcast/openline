// Package portaudio is a minimal PortAudio binding (output only, blocking
// API, 48 kHz float32) loaded at runtime via purego, so the bridge stays one
// cross-compiled binary. It reaches any sound card the OS exposes: USB
// interfaces, Dante Virtual Soundcard, BlackHole/Loopback, etc.
package portaudio

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"openline/internal/dl"
)

const (
	SampleRate = 48000
	paFloat32  = 0x1
	// Pa_WriteStream reports an underflow when the card ran dry; the data was
	// still written, so it isn't an error for us.
	paOutputUnderflowed = -9980
	paInputOverflowed   = -9981
)

// C layouts (identical on 64-bit Windows, macOS and Linux; see the comments
// on streamParameters for the one field whose C type differs).
type deviceInfo struct {
	structVersion int32
	_             int32
	name          *byte
	hostAPI       int32
	maxIn         int32
	maxOut        int32
	_             int32
	lowIn         float64
	lowOut        float64
	highIn        float64
	highOut       float64
	defaultRate   float64
}

type hostAPIInfo struct {
	structVersion int32
	typeID        int32
	name          *byte
	deviceCount   int32
	defaultIn     int32
	defaultOut    int32
}

type streamParameters struct {
	device   int32
	channels int32
	// C "unsigned long": 4 bytes on Windows, 8 elsewhere. It sits at offset 8
	// followed by an 8-aligned double, so an 8-byte little-endian field with
	// the high half zero matches both.
	format   uint64
	latency  float64
	hostInfo uintptr
}

var (
	loadOnce sync.Once
	loadErr  error

	paInitialize       func() int32
	paGetDeviceCount   func() int32
	paGetDeviceInfo    func(i int32) *deviceInfo
	paGetHostAPIInfo   func(i int32) *hostAPIInfo
	paGetDefaultOutput func() int32
	paGetDefaultInput  func() int32
	paOpenStream       func(stream *uintptr, in, out *streamParameters, rate float64, frames uint, flags uint, cb, user uintptr) int32
	paStartStream      func(s uintptr) int32
	paWriteStream      func(s uintptr, buf *float32, frames uint) int32
	paReadStream       func(s uintptr, buf *float32, frames uint) int32
	paStopStream       func(s uintptr) int32
	paCloseStream      func(s uintptr) int32
	paGetErrorText     func(code int32) *byte
)

func candidates() []string {
	if p := os.Getenv("PORTAUDIO_LIB"); p != "" {
		return []string{p}
	}
	switch runtime.GOOS {
	case "windows":
		return []string{"portaudio.dll", "libportaudio-2.dll", "libportaudio.dll", "portaudio_x64.dll"}
	case "darwin":
		return []string{"libportaudio.2.dylib", "/opt/homebrew/lib/libportaudio.2.dylib", "/usr/local/lib/libportaudio.2.dylib", "libportaudio.dylib"}
	default:
		return []string{"libportaudio.so.2", "libportaudio.so"}
	}
}

// Load loads and initialises PortAudio. Safe to call repeatedly.
func Load() error {
	loadOnce.Do(func() {
		h, err := dl.Open(candidates()...)
		if err != nil {
			loadErr = fmt.Errorf("PortAudio not found (needed for sound card outputs: Windows: portaudio.dll next to the bridge; macOS: brew install portaudio; Linux: apt install libportaudio2; or set PORTAUDIO_LIB): %w", err)
			return
		}
		reg := func(fn any, name string) { purego.RegisterLibFunc(fn, h, name) }
		reg(&paInitialize, "Pa_Initialize")
		reg(&paGetDeviceCount, "Pa_GetDeviceCount")
		reg(&paGetDeviceInfo, "Pa_GetDeviceInfo")
		reg(&paGetHostAPIInfo, "Pa_GetHostApiInfo")
		reg(&paGetDefaultOutput, "Pa_GetDefaultOutputDevice")
		reg(&paGetDefaultInput, "Pa_GetDefaultInputDevice")
		reg(&paOpenStream, "Pa_OpenStream")
		reg(&paStartStream, "Pa_StartStream")
		reg(&paWriteStream, "Pa_WriteStream")
		reg(&paReadStream, "Pa_ReadStream")
		reg(&paStopStream, "Pa_StopStream")
		reg(&paCloseStream, "Pa_CloseStream")
		reg(&paGetErrorText, "Pa_GetErrorText")
		if rc := paInitialize(); rc != 0 {
			loadErr = fmt.Errorf("Pa_Initialize: %s", errText(rc))
		}
	})
	return loadErr
}

func gostr(p *byte) string {
	if p == nil {
		return ""
	}
	var b []byte
	for ptr := unsafe.Pointer(p); *(*byte)(ptr) != 0; ptr = unsafe.Add(ptr, 1) {
		b = append(b, *(*byte)(ptr))
	}
	return string(b)
}

func errText(rc int32) string {
	if paGetErrorText == nil {
		return fmt.Sprint(rc)
	}
	return fmt.Sprintf("%s (%d)", gostr(paGetErrorText(rc)), rc)
}

// Device is a sound card the OS exposes. A card usually shows up once as an
// output and (on Windows, under a different name) once as an input.
type Device struct {
	Index       int
	Name        string
	HostAPI     string // e.g. "Windows WASAPI", "Core Audio", "ALSA"
	MaxOut      int
	MaxIn       int
	DefaultRate float64
	LowLatency  time.Duration // for the direction it was listed under
	Default     bool
	input       bool
}

func (d Device) String() string {
	def := ""
	if d.Default {
		def = "  (default)"
	}
	if d.input {
		return fmt.Sprintf("%-50s %2d inputs   %s%s", d.Name, d.MaxIn, d.HostAPI, def)
	}
	return fmt.Sprintf("%-50s %2d outputs  %s%s", d.Name, d.MaxOut, d.HostAPI, def)
}

func list(input bool) ([]Device, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	def := int(paGetDefaultOutput())
	if input {
		def = int(paGetDefaultInput())
	}
	var out []Device
	for i := int32(0); i < paGetDeviceCount(); i++ {
		di := paGetDeviceInfo(i)
		if di == nil || (input && di.maxIn <= 0) || (!input && di.maxOut <= 0) {
			continue
		}
		lat := di.lowOut
		if input {
			lat = di.lowIn
		}
		d := Device{Index: int(i), Name: gostr(di.name), MaxOut: int(di.maxOut), MaxIn: int(di.maxIn), DefaultRate: di.defaultRate,
			LowLatency: time.Duration(lat * float64(time.Second)), Default: int(i) == def, input: input}
		if hi := paGetHostAPIInfo(di.hostAPI); hi != nil {
			d.HostAPI = gostr(hi.name)
		}
		out = append(out, d)
	}
	return out, nil
}

// Outputs lists devices that have output channels.
func Outputs() ([]Device, error) { return list(false) }

// Inputs lists devices that have input channels.
func Inputs() ([]Device, error) { return list(true) }

// hostRank prefers the low-latency, multichannel-friendly host API when the
// same card shows up several times (Windows lists each card under MME,
// DirectSound and WASAPI).
func hostRank(h string) int {
	h = strings.ToLower(h)
	switch {
	case strings.Contains(h, "wasapi"), strings.Contains(h, "core audio"), strings.Contains(h, "alsa"):
		return 0
	case strings.Contains(h, "asio"), strings.Contains(h, "jack"):
		return 1
	case strings.Contains(h, "directsound"):
		return 2
	}
	return 3
}

// FindOutput picks an output by name: exact match first, then the first that
// contains name (case-insensitive). Empty name = the system default output.
func FindOutput(name string) (Device, error) { return find(name, false) }

// FindInput picks an input the same way. Empty name = the system default input.
func FindInput(name string) (Device, error) { return find(name, true) }

func find(name string, input bool) (Device, error) {
	devs, err := list(input)
	if err != nil {
		return Device{}, err
	}
	what := map[bool]string{true: "inputs", false: "outputs"}[input]
	if len(devs) == 0 {
		return Device{}, fmt.Errorf("no sound card %s found", what)
	}
	if name == "" {
		for _, d := range devs {
			if d.Default {
				return d, nil
			}
		}
		return devs[0], nil
	}
	n := strings.ToLower(strings.TrimSpace(name))
	best, found := Device{}, false
	for _, exact := range []bool{true, false} {
		for _, d := range devs {
			dn := strings.ToLower(d.Name)
			if (exact && dn == n) || (!exact && strings.Contains(dn, n)) {
				if !found || hostRank(d.HostAPI) < hostRank(best.HostAPI) || (hostRank(d.HostAPI) == hostRank(best.HostAPI) && d.MaxOut+d.MaxIn > best.MaxOut+best.MaxIn) {
					best, found = d, true
				}
			}
		}
		if found {
			return best, nil
		}
	}
	return Device{}, fmt.Errorf("no sound card %s matching %q (run openline-bridge -devices to list them)", what, name)
}

// Stream is an open, running 48 kHz float32 output stream.
type Stream struct {
	s        uintptr
	channels int
	Device   Device
}

// OpenOutput opens dev with the given number of channels (at most dev.MaxOut).
func OpenOutput(dev Device, channels int, latency time.Duration) (*Stream, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	if channels <= 0 || channels > dev.MaxOut {
		channels = dev.MaxOut
	}
	if latency < dev.LowLatency {
		latency = dev.LowLatency
	}
	p := streamParameters{device: int32(dev.Index), channels: int32(channels), format: paFloat32, latency: latency.Seconds()}
	var s uintptr
	if rc := paOpenStream(&s, nil, &p, SampleRate, 0, 0, 0, 0); rc != 0 {
		return nil, fmt.Errorf("open %q at 48 kHz with %d channels: %s", dev.Name, channels, errText(rc))
	}
	if rc := paStartStream(s); rc != 0 {
		paCloseStream(s)
		return nil, fmt.Errorf("start %q: %s", dev.Name, errText(rc))
	}
	return &Stream{s: s, channels: channels, Device: dev}, nil
}

// Channels is the number of interleaved channels Write expects.
func (st *Stream) Channels() int { return st.channels }

// Write plays interleaved samples (len = frames × Channels). It blocks until
// the card has room, so it paces the caller at the card's own clock.
func (st *Stream) Write(interleaved []float32) error {
	frames := len(interleaved) / st.channels
	if frames == 0 {
		return nil
	}
	rc := paWriteStream(st.s, &interleaved[0], uint(frames))
	runtime.KeepAlive(interleaved)
	if rc != 0 && rc != paOutputUnderflowed {
		return errors.New(errText(rc))
	}
	return nil
}

func (st *Stream) Close() {
	if st.s != 0 {
		paStopStream(st.s)
		paCloseStream(st.s)
		st.s = 0
	}
}

// InStream is an open, running float32 input stream.
type InStream struct {
	s        uintptr
	channels int
	Rate     int
	Device   Device
}

// OpenInput opens dev's first `channels` inputs. It tries 48 kHz, then the
// card's own default rate (the caller resamples).
func OpenInput(dev Device, channels int, latency time.Duration) (*InStream, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	if channels <= 0 {
		channels = 1
	}
	if channels > dev.MaxIn {
		return nil, fmt.Errorf("%q has %d inputs, %d needed", dev.Name, dev.MaxIn, channels)
	}
	if latency < dev.LowLatency {
		latency = dev.LowLatency
	}
	p := streamParameters{device: int32(dev.Index), channels: int32(channels), format: paFloat32, latency: latency.Seconds()}
	var lastErr error
	for _, rate := range []float64{SampleRate, dev.DefaultRate} {
		if rate <= 0 {
			continue
		}
		var s uintptr
		if rc := paOpenStream(&s, &p, nil, rate, 0, 0, 0, 0); rc != 0 {
			lastErr = fmt.Errorf("open input %q at %.0f Hz with %d channels: %s", dev.Name, rate, channels, errText(rc))
			continue
		}
		if rc := paStartStream(s); rc != 0 {
			paCloseStream(s)
			lastErr = fmt.Errorf("start input %q: %s", dev.Name, errText(rc))
			continue
		}
		return &InStream{s: s, channels: channels, Rate: int(rate), Device: dev}, nil
	}
	return nil, lastErr
}

// Channels is the number of interleaved channels Read fills.
func (st *InStream) Channels() int { return st.channels }

// Read fills interleaved (len = frames × Channels), blocking until the card
// has delivered that much.
func (st *InStream) Read(interleaved []float32) error {
	frames := len(interleaved) / st.channels
	if frames == 0 {
		return nil
	}
	rc := paReadStream(st.s, &interleaved[0], uint(frames))
	runtime.KeepAlive(interleaved)
	if rc != 0 && rc != paInputOverflowed {
		return errors.New(errText(rc))
	}
	return nil
}

func (st *InStream) Close() {
	if st.s != 0 {
		paStopStream(st.s)
		paCloseStream(st.s)
		st.s = 0
	}
}
