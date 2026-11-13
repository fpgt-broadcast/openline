package portaudio

import (
	"testing"
	"unsafe"
)

// The structs mirror PortAudio's C layouts on 64-bit platforms.
func TestLayouts(t *testing.T) {
	var d deviceInfo
	var h hostAPIInfo
	var p streamParameters
	checks := []struct {
		what      string
		got, want uintptr
	}{
		{"deviceInfo.name", unsafe.Offsetof(d.name), 8},
		{"deviceInfo.hostAPI", unsafe.Offsetof(d.hostAPI), 16},
		{"deviceInfo.maxOut", unsafe.Offsetof(d.maxOut), 24},
		{"deviceInfo.lowIn", unsafe.Offsetof(d.lowIn), 32},
		{"deviceInfo.lowOut", unsafe.Offsetof(d.lowOut), 40},
		{"deviceInfo.defaultRate", unsafe.Offsetof(d.defaultRate), 64},
		{"hostAPIInfo.name", unsafe.Offsetof(h.name), 8},
		{"hostAPIInfo.defaultOut", unsafe.Offsetof(h.defaultOut), 24},
		{"streamParameters.format", unsafe.Offsetof(p.format), 8},
		{"streamParameters.latency", unsafe.Offsetof(p.latency), 16},
		{"streamParameters.hostInfo", unsafe.Offsetof(p.hostInfo), 24},
		{"sizeof(streamParameters)", unsafe.Sizeof(p), 32},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s at %d, want %d", c.what, c.got, c.want)
		}
	}
}
