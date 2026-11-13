package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"openline/internal/proto"
)

func TestShowsValidate(t *testing.T) {
	// A config from before shows existed gets one "Main" show.
	old := Config{
		Channels: []Channel{{ID: 1, Name: "Production", Color: "#ff0000"}},
		Users:    []User{{Name: "Director", PIN: "1"}},
	}
	if err := old.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(old.Shows) != 1 || old.Shows[0].Name != "Main" || old.Channels[0].Show != 1 || old.Users[0].Show != 1 {
		t.Fatalf("migration: %+v %+v %+v", old.Shows, old.Channels, old.Users)
	}

	base := func() Config {
		return Config{
			Shows:    []Show{{ID: 1, Name: "Main"}, {ID: 2, Name: "Show B"}},
			Channels: []Channel{{ID: 1, Name: "Production", Show: 1}, {ID: 2, Name: "Production", Show: 2}, {ID: 3, Name: "Stray", Show: 9}},
			Users: []User{
				{Name: "Camera 1", PIN: "2222", Show: 1},
				{Name: "Camera 1", PIN: "3333", Show: 2},
			},
		}
	}
	c := base()
	if err := c.Validate(); err != nil {
		t.Fatalf("same name in two shows with different PINs should be fine: %v", err)
	}
	if c.Channels[2].Show != 1 {
		t.Fatalf("channel in an unknown show should move to the first show, got %d", c.Channels[2].Show)
	}

	c = base()
	c.Users[1].PIN = "2222"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "same PIN") {
		t.Fatalf("same name + PIN across shows must be rejected, got %v", err)
	}
	c = base()
	c.Users[1].Show = 1
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "called") {
		t.Fatalf("same name twice in one show must be rejected, got %v", err)
	}
	c = base()
	c.Shows[1].Name = "main"
	if err := c.Validate(); err == nil {
		t.Fatal("two shows with the same name must be rejected")
	}

	// Outputs: an empty output is dropped; at most 32.
	c = base()
	c.Channels[0].Out = &ChannelOut{}
	c.Channels[1].Out = &ChannelOut{Card: 99}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Channels[0].Out != nil || c.Channels[1].Out.Card != 64 {
		t.Fatalf("outputs not normalised: %+v %+v", c.Channels[0].Out, c.Channels[1].Out)
	}
	c = base()
	c.Channels = nil
	for i := 1; i <= 33; i++ {
		c.Channels = append(c.Channels, Channel{ID: i, Name: "ch", Show: 1, Out: &ChannelOut{NDI: true}})
	}
	if err := c.Validate(); err == nil {
		t.Fatal("33 outputs must be rejected")
	}
}

// Two shows run at once: talk stays inside each show, the same assignment name
// exists in both, and the bridge's outputs login hears every output channel.
func TestShowsIsolationAndOutputs(t *testing.T) {
	h := newHarness(t)
	var cfg Config
	h.do("GET", "/api/config", nil, &cfg, 200)
	cfg.Shows = append(cfg.Shows, Show{ID: 2, Name: "Show B"})
	cfg.Channels = append(cfg.Channels, Channel{ID: 11, Name: "Production", Color: "#e5484d", Show: 2, Out: &ChannelOut{Card: 2}})
	cfg.channel(1).Out = &ChannelOut{NDI: true}
	cfg.Users = append(cfg.Users,
		User{Name: "Director", PIN: "9999", Show: 2, Channels: []Access{{Ch: 11, Talk: true, Listen: true}}},
		User{Name: "Camera 1", PIN: "8888", Show: 2, Channels: []Access{{Ch: 11, Talk: true, Listen: true}}},
		User{Name: "Engineer", PIN: "7777", Show: 1, Channels: []Access{{Ch: 1, Listen: true}, {Ch: 11, Listen: true}}},
		User{Name: "Outputs", PIN: "6666", Outputs: true},
	)
	h.do("PUT", "/api/config", cfg, &cfg, 200)

	dirA, _ := h.connect(proto.Msg{User: "Director", PIN: "1111", Client: "test"})
	dirB, _ := h.connect(proto.Msg{User: "Director", PIN: "9999", Client: "test"})
	_, camA := h.connect(proto.Msg{User: "Camera 1", PIN: "2222", Client: "test"})
	camBc, camB := h.connect(proto.Msg{User: "Camera 1", PIN: "8888", Client: "test"})
	outc, out := h.connect(proto.Msg{User: "Outputs", PIN: "6666", Client: "bridge"})
	engc, _ := h.connect(proto.Msg{User: "Engineer", PIN: "7777", Client: "beltpack"})

	if w := camBc.Welcome(); len(w.Channels) != 1 || w.Channels[0].ID != 11 || w.Channels[0].Show != "Show B" {
		t.Fatalf("show B camera welcome: %+v", w.Channels)
	}
	// A beltpack whose channels span two shows gets the show in each key name.
	if w := engc.Welcome(); w.Channels[0].Name != "Main: Production" || w.Channels[1].Name != "SB: Production" {
		t.Fatalf("beltpack names: %q %q", w.Channels[0].Name, w.Channels[1].Name)
	}
	// The outputs login hears exactly the channels with an output, with where to send them.
	ow := outc.Welcome()
	if len(ow.Channels) != 2 || ow.Channels[0].ID != 1 || !ow.Channels[0].Out.NDI || ow.Channels[1].Out.Card != 2 || ow.Channels[1].Show != "Show B" {
		b, _ := json.Marshal(ow.Channels)
		t.Fatalf("outputs welcome: %s", b)
	}
	if len(ow.Listen) != 2 {
		t.Fatalf("outputs login should listen to all its channels, got %v", ow.Listen)
	}

	// Show B's director talks on Show B Production: only Show B hears it.
	dirB.SetTalk(11, true)
	time.Sleep(60 * time.Millisecond)
	dirB.SendAudio(1, []byte("show-b"))
	eventually(t, "show B camera hears show B", func() bool { return camB.nAudio() == 1 })
	eventually(t, "outputs hear show B", func() bool { return out.nAudio() == 1 })
	// Show A's director on Main Production.
	dirA.SetTalk(1, true)
	time.Sleep(60 * time.Millisecond)
	dirA.SendAudio(1, []byte("show-a"))
	eventually(t, "show A camera hears show A", func() bool { return camA.nAudio() == 1 })
	eventually(t, "outputs hear show A", func() bool { return out.nAudio() == 2 })
	time.Sleep(60 * time.Millisecond)
	if camA.nAudio() != 1 || camB.nAudio() != 1 {
		t.Fatalf("talk leaked between shows: A=%d B=%d", camA.nAudio(), camB.nAudio())
	}
	out.mu.Lock()
	if out.masks[0] != 0b10 || out.masks[1] != 0b01 {
		t.Fatalf("outputs masks = %b %b (want Show B on bit 1, Main on bit 0)", out.masks[0], out.masks[1])
	}
	out.mu.Unlock()

	// Admin sessions list carries each login's show.
	var sessions []SessionInfo
	h.do("GET", "/api/sessions", nil, &sessions, 200)
	shows := map[string]int{}
	for _, s := range sessions {
		shows[s.User+"/"+s.Person] = s.Show
	}
	n2 := 0
	for _, s := range sessions {
		if s.Show == 2 {
			n2++
		}
	}
	if n2 != 2 {
		t.Fatalf("expected 2 show-B sessions, got %d (%v)", n2, shows)
	}
}
