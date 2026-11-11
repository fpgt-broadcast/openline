// Package proto defines the OpenLine wire protocol shared by the server,
// the NDI bridge and test clients. See docs/PROTOCOL.md for the full spec.
//
// Transport: one WebSocket per client session at /ws.
//   - Text frames carry JSON control messages (field "t" is the type).
//   - Binary frames carry Opus audio, 20 ms per packet.
package proto

import (
	"encoding/binary"
	"errors"
)

const Version = 1

// Binary frame kinds (first byte of every binary WebSocket message).
const (
	// Client -> server: [0x01][seq u16 BE][opus packet]
	KindUplink byte = 0x01
	// Server -> client: [0x02][src session u16][seq u16][mask u32][opus packet]
	// mask bit i set => audio is heard on the listener's channel at index i
	// (index into the "channels" array of the welcome message).
	KindDownlink byte = 0x02
)

const (
	UplinkHeader   = 3
	DownlinkHeader = 9
	MaxOpusPacket  = 1275
)

var ErrShort = errors.New("proto: short frame")

// EncodeUplink builds a client->server audio frame.
func EncodeUplink(seq uint16, opus []byte) []byte {
	b := make([]byte, UplinkHeader+len(opus))
	b[0] = KindUplink
	binary.BigEndian.PutUint16(b[1:], seq)
	copy(b[3:], opus)
	return b
}

// DecodeUplink parses a client->server audio frame.
func DecodeUplink(b []byte) (seq uint16, opus []byte, err error) {
	if len(b) < UplinkHeader+1 || b[0] != KindUplink {
		return 0, nil, ErrShort
	}
	return binary.BigEndian.Uint16(b[1:]), b[3:], nil
}

// EncodeDownlink builds a server->client audio frame.
func EncodeDownlink(src, seq uint16, mask uint32, opus []byte) []byte {
	b := make([]byte, DownlinkHeader+len(opus))
	b[0] = KindDownlink
	binary.BigEndian.PutUint16(b[1:], src)
	binary.BigEndian.PutUint16(b[3:], seq)
	binary.BigEndian.PutUint32(b[5:], mask)
	copy(b[9:], opus)
	return b
}

// DecodeDownlink parses a server->client audio frame.
func DecodeDownlink(b []byte) (src, seq uint16, mask uint32, opus []byte, err error) {
	if len(b) < DownlinkHeader+1 || b[0] != KindDownlink {
		return 0, 0, 0, nil, ErrShort
	}
	return binary.BigEndian.Uint16(b[1:]), binary.BigEndian.Uint16(b[3:]),
		binary.BigEndian.Uint32(b[5:]), b[9:], nil
}

// ---- JSON control messages ----

// Msg is a generic control message. Only the fields relevant to a given "t"
// are populated; everything else is omitted.
type Msg struct {
	T string `json:"t"`

	// hello
	Token  string `json:"token,omitempty"`
	User   string `json:"user,omitempty"`
	PIN    string `json:"pin,omitempty"`
	Client string `json:"client,omitempty"` // web | beltpack | bridge | test
	Ver    int    `json:"ver,omitempty"`
	Device string `json:"device,omitempty"` // hardware id (MAC) for beltpacks

	// talk / listen / call
	Ch int   `json:"ch,omitempty"`
	On *bool `json:"on,omitempty"`

	// ping / pong
	TS float64 `json:"ts,omitempty"`

	// error
	Msg string `json:"msg,omitempty"`

	// remote (server -> client, admin initiated): op = mute | vol | identify | reboot
	Op string `json:"op,omitempty"`
	V  *int   `json:"v,omitempty"`
}

// Telemetry is reported periodically (every ~5 s) by beltpacks.
type Telemetry struct {
	T        string `json:"t"`              // "telemetry"
	Battery  int    `json:"bat"`            // percent, -1 = mains/PoE
	Charging bool   `json:"chg,omitempty"`  //
	RSSI     int    `json:"rssi,omitempty"` // dBm (wifi)
	Net      string `json:"net,omitempty"`  // wifi | ethernet | ble
	IP       string `json:"ip,omitempty"`
	FW       string `json:"fw,omitempty"`
	Uptime   int    `json:"up,omitempty"` // seconds
	Headset  bool   `json:"hs,omitempty"` // headset plugged in
	PoE      bool   `json:"poe,omitempty"`
	Display  string `json:"disp,omitempty"` // oled | eink
}

// Assign tells a beltpack to log in as another user (admin re-assignment).
type Assign struct {
	T     string `json:"t"` // "assign"
	Token string `json:"token"`
	User  string `json:"user"`
}

// NetCfg tells a beltpack to store new network settings and reboot.
type NetCfg struct {
	T       string `json:"t"` // "netcfg"
	Mode    string `json:"mode"`
	DHCP    bool   `json:"dhcp"`
	IP      string `json:"ip,omitempty"`
	Mask    string `json:"mask,omitempty"`
	Gateway string `json:"gw,omitempty"`
	DNS     string `json:"dns,omitempty"`
	SSID    string `json:"ssid,omitempty"`
	Pass    string `json:"pass,omitempty"`
	Server  string `json:"server,omitempty"`
}

// Settings pushes audio/display settings to a beltpack.
type Settings struct {
	T        string `json:"t"` // "settings"
	Name     string `json:"name"`
	MicType  string `json:"micType"`
	MicGain  int    `json:"micGain"`
	Sidetone int    `json:"sidetone"`
	HPLevel  int    `json:"hpLevel"`
	Bright   int    `json:"bright"`
}

// ChannelInfo describes one channel as seen by a particular user.
type ChannelInfo struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Color      string `json:"color"`
	CanTalk    bool   `json:"canTalk"`
	CanListen  bool   `json:"canListen"`
	ListenOnly bool   `json:"listenOnly,omitempty"` // e.g. program feed
	Program    bool   `json:"program,omitempty"`    // ducked under intercom traffic
}

// Welcome is sent after a successful hello, and again whenever the server
// configuration changes (clients must rebuild their channel list).
type Welcome struct {
	T        string        `json:"t"` // "welcome"
	SID      uint16        `json:"sid"`
	Server   string        `json:"server"`
	User     string        `json:"user"`
	Admin    bool          `json:"admin,omitempty"`
	Channels []ChannelInfo `json:"channels"`
	Talk     []int         `json:"talk"`   // channel ids currently talking
	Listen   []int         `json:"listen"` // channel ids currently listening
	Muted    bool          `json:"muted,omitempty"`
	// TallyScreen: the admin wants this user's app in the full-screen tally view.
	TallyScreen bool `json:"tallyScreen,omitempty"`
}

// Talker is a session currently talking on a channel.
type Talker struct {
	SID  uint16 `json:"sid"`
	Name string `json:"name"`
}

// ChannelState is live activity on a channel.
type ChannelState struct {
	ID      int      `json:"id"`
	Talkers []Talker `json:"talkers"`
	Calls   []Talker `json:"calls"`
	Members []Talker `json:"members"` // sessions listening
}

// State is broadcast whenever talk/call activity changes.
type State struct {
	T        string         `json:"t"` // "state"
	Channels []ChannelState `json:"channels"`
}

// Self echoes the server-authoritative talk/listen sets of the session.
type Self struct {
	T      string `json:"t"` // "self"
	Talk   []int  `json:"talk"`
	Listen []int  `json:"listen"`
	Muted  bool   `json:"muted"` // mic muted by admin
}

// TallyUpdate is sent by a bridge (role "NDI bridge") with the switcher's
// current program and preview inputs (vMix input numbers).
type TallyUpdate struct {
	T       string `json:"t"` // "tally"
	Program []int  `json:"program"`
	Preview []int  `json:"preview"`
}

// Tally tells a client its own on-air state: "program", "preview" or "off".
type Tally struct {
	T     string `json:"t"` // "tally"
	State string `json:"state"`
}
