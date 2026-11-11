package ndi

import (
	"testing"
	"unsafe"
)

// Guards the hand-written C struct layouts against accidental edits.
func TestStructLayout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("64-bit only")
	}
	var a cAudioFrameV3
	check := func(name string, got, want uintptr) {
		if got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	check("sizeof(audio_frame_v3)", unsafe.Sizeof(a), 64)
	check("audio.timecode", unsafe.Offsetof(a.timecode), 16)
	check("audio.FourCC", unsafe.Offsetof(a.fourCC), 24)
	check("audio.p_data", unsafe.Offsetof(a.data), 32)
	check("audio.stride", unsafe.Offsetof(a.strideBytes), 40)
	check("audio.p_metadata", unsafe.Offsetof(a.metadata), 48)
	check("audio.timestamp", unsafe.Offsetof(a.timestamp), 56)
	var r cRecvCreateV3
	check("sizeof(recv_create_v3)", unsafe.Sizeof(r), 40)
	check("recv.color_format", unsafe.Offsetof(r.colorFormat), 16)
	check("recv.bandwidth", unsafe.Offsetof(r.bandwidth), 20)
	check("recv.allow_video_fields", unsafe.Offsetof(r.allowFields), 24)
	check("recv.p_ndi_recv_name", unsafe.Offsetof(r.recvName), 32)
	var f cFindCreate
	check("find.p_groups", unsafe.Offsetof(f.groups), 8)
	check("find.p_extra_ips", unsafe.Offsetof(f.extraIPs), 16)
	var s cSendCreate
	check("send.clock_video", unsafe.Offsetof(s.clockVideo), 16)
	check("send.clock_audio", unsafe.Offsetof(s.clockAudio), 17)
}
