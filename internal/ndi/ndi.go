// Package ndi is a minimal binding to the NDI® runtime, loaded at run time
// (no cgo, no SDK needed to build). Users install the free NDI Runtime / NDI
// Tools; the library is found via NDI_RUNTIME_DIR_V6/V5 or standard paths.
// Only audio is used: receive (audio-only bandwidth) and send.
//
// NDI® is a registered trademark of Vizrt NDI AB.
package ndi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"openline/internal/dl"
)

// C struct layouts (64-bit). Field order and types mirror Processing.NDI.*.h.

type cSource struct {
	name *byte // p_ndi_name
	url  *byte // p_url_address
}

type cFindCreate struct {
	showLocal bool
	_         [7]byte
	groups    *byte
	extraIPs  *byte
}

type cRecvCreateV3 struct {
	source      cSource
	colorFormat int32
	bandwidth   int32
	allowFields bool
	_           [7]byte
	recvName    *byte
}

type cAudioFrameV3 struct {
	sampleRate  int32
	channels    int32
	samples     int32
	_           int32
	timecode    int64
	fourCC      uint32
	_           uint32
	data        unsafe.Pointer
	strideBytes int32 // channel_stride_in_bytes
	_           int32
	metadata    *byte
	timestamp   int64
}

type cSendCreate struct {
	name       *byte
	groups     *byte
	clockVideo bool
	clockAudio bool
}

const (
	frameNone  = 0
	frameAudio = 2
	frameError = 4

	bandwidthAudioOnly = 10
	colorFastest       = 100

	fourCCFLTP         = uint32('F') | uint32('L')<<8 | uint32('T')<<16 | uint32('p')<<24
	timecodeSynthesize = int64(^uint64(0) >> 1) // INT64_MAX
)

var (
	loadOnce sync.Once
	loadErr  error

	fnInitialize      func() bool
	fnFindCreate      func(p *cFindCreate) uintptr
	fnFindWait        func(f uintptr, timeoutMs uint32) bool
	fnFindSources     func(f uintptr, n *uint32) unsafe.Pointer
	fnFindDestroy     func(f uintptr)
	fnRecvCreate      func(p *cRecvCreateV3) uintptr
	fnRecvCapture     func(r uintptr, video, audio unsafe.Pointer, meta unsafe.Pointer, timeoutMs uint32) int32
	fnRecvFreeAudio   func(r uintptr, audio *cAudioFrameV3)
	fnRecvDestroy     func(r uintptr)
	fnSendCreate      func(p *cSendCreate) uintptr
	fnSendAudio       func(s uintptr, audio *cAudioFrameV3)
	fnSendDestroy     func(s uintptr)
	fnSendConnections func(s uintptr, timeoutMs uint32) int32
)

func candidates() []string {
	if p := os.Getenv("NDI_LIB"); p != "" {
		return []string{p}
	}
	var out []string
	lib := map[string]string{"windows": "Processing.NDI.Lib.x64.dll", "darwin": "libndi.dylib"}[runtime.GOOS]
	if lib == "" {
		lib = "libndi.so.6"
	}
	for _, env := range []string{"NDI_RUNTIME_DIR_V6", "NDI_RUNTIME_DIR_V5"} {
		if d := os.Getenv(env); d != "" {
			out = append(out, filepath.Join(d, lib))
		}
	}
	switch runtime.GOOS {
	case "windows":
		out = append(out, lib, `C:\Program Files\NDI\NDI 6 Runtime\v6\Processing.NDI.Lib.x64.dll`, `C:\Program Files\NDI\NDI 5 Runtime\v5\Processing.NDI.Lib.x64.dll`)
	case "darwin":
		out = append(out, "/usr/local/lib/libndi.dylib", "/Library/NDI SDK for Apple/lib/macOS/libndi.dylib", "/Applications/NDI Video Monitor.app/Contents/Frameworks/libndi.dylib")
	default:
		out = append(out, "libndi.so.6", "libndi.so.5", "libndi.so", "/usr/lib/libndi.so", "/usr/local/lib/libndi.so")
	}
	return out
}

// Load loads and initialises the NDI runtime.
func Load() error {
	loadOnce.Do(func() {
		h, err := dl.Open(candidates()...)
		if err != nil {
			loadErr = fmt.Errorf("NDI runtime not found — install NDI Tools / NDI Runtime from ndi.video, or set NDI_LIB: %w", err)
			return
		}
		reg := func(fn any, name string) { purego.RegisterLibFunc(fn, h, name) }
		reg(&fnInitialize, "NDIlib_initialize")
		reg(&fnFindCreate, "NDIlib_find_create_v2")
		reg(&fnFindWait, "NDIlib_find_wait_for_sources")
		reg(&fnFindSources, "NDIlib_find_get_current_sources")
		reg(&fnFindDestroy, "NDIlib_find_destroy")
		reg(&fnRecvCreate, "NDIlib_recv_create_v3")
		reg(&fnRecvCapture, "NDIlib_recv_capture_v3")
		reg(&fnRecvFreeAudio, "NDIlib_recv_free_audio_v3")
		reg(&fnRecvDestroy, "NDIlib_recv_destroy")
		reg(&fnSendCreate, "NDIlib_send_create")
		reg(&fnSendAudio, "NDIlib_send_send_audio_v3")
		reg(&fnSendDestroy, "NDIlib_send_destroy")
		reg(&fnSendConnections, "NDIlib_send_get_no_connections")
		if !fnInitialize() {
			loadErr = errors.New("NDIlib_initialize failed (unsupported CPU?)")
		}
	})
	return loadErr
}

func cstr(s string) *byte {
	b := append([]byte(s), 0)
	return &b[0]
}

func gostr(p *byte) string {
	if p == nil {
		return ""
	}
	var n int
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}

// Source is an NDI source on the network.
type Source struct{ Name, URL string }

// FindSources lists NDI sources visible within the given wait time.
// extraIPs is a comma separated list of IPs to probe (for routed networks).
func FindSources(wait time.Duration, extraIPs string) ([]Source, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	fc := cFindCreate{showLocal: true}
	if extraIPs != "" {
		fc.extraIPs = cstr(extraIPs)
	}
	f := fnFindCreate(&fc)
	runtime.KeepAlive(fc.extraIPs)
	if f == 0 {
		return nil, errors.New("NDIlib_find_create_v2 failed")
	}
	defer fnFindDestroy(f)
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		fnFindWait(f, 500)
	}
	var n uint32
	p := fnFindSources(f, &n)
	var out []Source
	if p != nil && n > 0 {
		for _, s := range unsafe.Slice((*cSource)(p), n) {
			out = append(out, Source{Name: gostr(s.name), URL: gostr(s.url)})
		}
	}
	return out, nil
}

// Receiver pulls audio from one NDI source.
type Receiver struct {
	r       uintptr
	frame   cAudioFrameV3
	Picks   []int // channel indices to mix to mono (empty = all)
	srcName string
}

// NewReceiver finds a source by exact name (or unique substring) and connects.
func NewReceiver(name, extraIPs string, picks []int) (*Receiver, error) {
	srcs, err := FindSources(3*time.Second, extraIPs)
	if err != nil {
		return nil, err
	}
	var match *Source
	for i := range srcs {
		if strings.EqualFold(srcs[i].Name, name) {
			match = &srcs[i]
			break
		}
	}
	if match == nil {
		for i := range srcs {
			if strings.Contains(strings.ToLower(srcs[i].Name), strings.ToLower(name)) {
				if match != nil {
					return nil, fmt.Errorf("NDI source %q is ambiguous (%q, %q)", name, match.Name, srcs[i].Name)
				}
				match = &srcs[i]
			}
		}
	}
	if match == nil {
		var names []string
		for _, s := range srcs {
			names = append(names, s.Name)
		}
		return nil, fmt.Errorf("NDI source %q not found; visible: %v", name, names)
	}
	rc := cRecvCreateV3{
		source:      cSource{name: cstr(match.Name)},
		colorFormat: colorFastest,
		bandwidth:   bandwidthAudioOnly,
		allowFields: true,
		recvName:    cstr("OpenLine Bridge"),
	}
	if match.URL != "" {
		rc.source.url = cstr(match.URL)
	}
	r := fnRecvCreate(&rc)
	runtime.KeepAlive(rc.source.name)
	runtime.KeepAlive(rc.source.url)
	runtime.KeepAlive(rc.recvName)
	if r == 0 {
		return nil, errors.New("NDIlib_recv_create_v3 failed")
	}
	return &Receiver{r: r, Picks: picks, srcName: match.Name}, nil
}

func (r *Receiver) SourceName() string { return r.srcName }

// Read blocks up to timeout and returns mono samples and their sample rate.
// It returns (nil, 0, nil) on timeout.
func (r *Receiver) Read(timeout time.Duration) ([]float32, int, error) {
	t := fnRecvCapture(r.r, nil, unsafe.Pointer(&r.frame), nil, uint32(timeout/time.Millisecond))
	switch t {
	case frameAudio:
	case frameError:
		return nil, 0, errors.New("NDI receiver error (source lost)")
	default:
		return nil, 0, nil
	}
	defer fnRecvFreeAudio(r.r, &r.frame)
	f := &r.frame
	n := int(f.samples)
	if n <= 0 || f.channels <= 0 || f.data == nil {
		return nil, 0, nil
	}
	stride := int(f.strideBytes)
	if stride == 0 {
		stride = n * 4
	}
	picks := r.Picks
	if len(picks) == 0 {
		picks = make([]int, f.channels)
		for i := range picks {
			picks[i] = i
		}
	}
	out := make([]float32, n)
	used := 0
	for _, c := range picks {
		if c < 0 || c >= int(f.channels) {
			continue
		}
		plane := unsafe.Slice((*float32)(unsafe.Add(f.data, c*stride)), n)
		for i, v := range plane {
			out[i] += v
		}
		used++
	}
	if used > 1 {
		g := 1 / float32(used)
		for i := range out {
			out[i] *= g
		}
	}
	return out, int(f.sampleRate), nil
}

func (r *Receiver) Close() {
	if r.r != 0 {
		fnRecvDestroy(r.r)
		r.r = 0
	}
}

// Sender publishes an audio-only NDI source.
type Sender struct {
	s    uintptr
	buf  []float32
	name string
}

// NewSender creates an NDI source with the given name. Audio is sent as
// 48 kHz stereo (mono duplicated) because most consumers expect stereo.
func NewSender(name string) (*Sender, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	sc := cSendCreate{name: cstr(name), clockAudio: false}
	s := fnSendCreate(&sc)
	runtime.KeepAlive(sc.name)
	if s == 0 {
		return nil, errors.New("NDIlib_send_create failed")
	}
	return &Sender{s: s, name: name}, nil
}

// Write sends one block of 48 kHz mono samples.
func (s *Sender) Write(mono []float32) {
	n := len(mono)
	if cap(s.buf) < 2*n {
		s.buf = make([]float32, 2*n)
	}
	buf := s.buf[:2*n]
	copy(buf, mono)
	copy(buf[n:], mono)
	f := cAudioFrameV3{
		sampleRate:  48000,
		channels:    2,
		samples:     int32(n),
		timecode:    timecodeSynthesize,
		fourCC:      fourCCFLTP,
		data:        unsafe.Pointer(&buf[0]),
		strideBytes: int32(n * 4),
	}
	fnSendAudio(s.s, &f)
	runtime.KeepAlive(buf)
}

// Connections returns how many receivers are connected to this sender.
func (s *Sender) Connections() int { return int(fnSendConnections(s.s, 0)) }

func (s *Sender) Close() {
	if s.s != 0 {
		fnSendDestroy(s.s)
		s.s = 0
	}
}
