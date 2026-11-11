package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// Channel is a party line. Everyone listening hears everyone talking on it.
type Channel struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Color      string `json:"color"`
	ListenOnly bool   `json:"listenOnly,omitempty"` // nobody may talk (program/IFB feeds; bridges are exempt)
	Program    bool   `json:"program,omitempty"`    // clients duck this under intercom traffic
}

// Access grants a user rights on a channel. Order of a user's Access list is
// the order the channels appear on keys (beltpack key 1 = first entry, etc).
type Access struct {
	Ch     int  `json:"ch"`
	Talk   bool `json:"talk"`
	Listen bool `json:"listen"`
}

// User is a login identity (a person, a beltpack, or a bridge source).
type User struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	PIN      string   `json:"pin,omitempty"`   // optional; empty = token login only
	Token    string   `json:"token"`           // used in invite links, devices and bridges
	Admin    bool     `json:"admin,omitempty"` // can open the admin page with their PIN
	Bridge   bool     `json:"bridge,omitempty"`
	Channels []Access `json:"channels"`
	// Tally lists the vMix input numbers this user is (e.g. a camera op on
	// input 2). The user's devices show LIVE/PREVIEW from those inputs.
	Tally []int `json:"tally,omitempty"`
	// TallyScreen starts this user's app in the full-screen tally view.
	TallyScreen bool `json:"tallyScreen,omitempty"`
}

// NetConfig is a beltpack's network setup, pushed to the device by the admin.
type NetConfig struct {
	Mode    string `json:"mode"` // wifi | ethernet | ble
	DHCP    bool   `json:"dhcp"`
	IP      string `json:"ip,omitempty"`
	Mask    string `json:"mask,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	DNS     string `json:"dns,omitempty"`
	SSID    string `json:"ssid,omitempty"`
	Pass    string `json:"pass,omitempty"`   // never returned by the API
	Server  string `json:"server,omitempty"` // ws(s)://host/ws; empty = keep current
}

// DeviceAudio holds per-beltpack audio settings, pushed on every connect.
type DeviceAudio struct {
	MicType  string `json:"micType"`  // dynamic | electret
	MicGain  int    `json:"micGain"`  // 0..100
	Sidetone int    `json:"sidetone"` // 0..100
	HPLevel  int    `json:"hpLevel"`  // 0..100 headphone output trim
	Bright   int    `json:"bright"`   // 0..100 display/LED brightness
}

// Device is a hardware beltpack known to the server (keyed by its MAC).
type Device struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	UserID string      `json:"userId,omitempty"`
	Net    NetConfig   `json:"net"`
	Audio  DeviceAudio `json:"audio"`
}

// Config is the full persisted server configuration.
type Config struct {
	ServerName string `json:"serverName"`
	// PublicURL is how off-site clients reach the server, e.g.
	// https://intercom.example.com. Used for invite links and QR codes.
	PublicURL     string    `json:"publicURL,omitempty"`
	AdminPassHash string    `json:"adminPassHash"`
	Channels      []Channel `json:"channels"`
	Users         []User    `json:"users"`
	Devices       []Device  `json:"devices"`
}

func (c *Config) userByID(id string) *User {
	for i := range c.Users {
		if c.Users[i].ID == id {
			return &c.Users[i]
		}
	}
	return nil
}

func (c *Config) device(id string) *Device {
	for i := range c.Devices {
		if c.Devices[i].ID == id {
			return &c.Devices[i]
		}
	}
	return nil
}

func defaultDeviceAudio() DeviceAudio {
	return DeviceAudio{MicType: "dynamic", MicGain: 70, Sidetone: 20, HPLevel: 80, Bright: 80}
}

func (c *Config) channel(id int) *Channel {
	for i := range c.Channels {
		if c.Channels[i].ID == id {
			return &c.Channels[i]
		}
	}
	return nil
}

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Validate normalizes the config and returns an error on inconsistencies.
func (c *Config) Validate() error {
	c.ServerName = strings.TrimSpace(c.ServerName)
	if c.ServerName == "" {
		c.ServerName = "OpenLine"
	}
	c.PublicURL = strings.TrimRight(strings.TrimSpace(c.PublicURL), "/")
	seenCh := map[int]bool{}
	for i := range c.Channels {
		ch := &c.Channels[i]
		ch.Name = strings.TrimSpace(ch.Name)
		if ch.ID <= 0 {
			return fmt.Errorf("channel %q: id must be > 0", ch.Name)
		}
		if seenCh[ch.ID] {
			return fmt.Errorf("duplicate channel id %d", ch.ID)
		}
		seenCh[ch.ID] = true
		if ch.Name == "" {
			return fmt.Errorf("channel %d: name required", ch.ID)
		}
		if !colorRe.MatchString(ch.Color) {
			ch.Color = "#4a90d9"
		}
	}
	seenName := map[string]bool{}
	seenTok := map[string]bool{}
	seenID := map[string]bool{}
	for i := range c.Users {
		u := &c.Users[i]
		u.Name = strings.TrimSpace(u.Name)
		if u.Name == "" {
			return errors.New("user name required")
		}
		key := strings.ToLower(u.Name)
		if seenName[key] {
			return fmt.Errorf("duplicate user name %q", u.Name)
		}
		seenName[key] = true
		if u.ID == "" {
			u.ID = randHex(6)
		}
		if seenID[u.ID] {
			u.ID = randHex(6)
		}
		seenID[u.ID] = true
		if len(u.Token) < 16 {
			u.Token = randHex(16)
		}
		if seenTok[u.Token] {
			return fmt.Errorf("duplicate token for user %q", u.Name)
		}
		seenTok[u.Token] = true
		// drop access entries for unknown/duplicate channels
		dedup := u.Channels[:0]
		seen := map[int]bool{}
		for _, a := range u.Channels {
			if !seenCh[a.Ch] || seen[a.Ch] {
				continue
			}
			seen[a.Ch] = true
			dedup = append(dedup, a)
		}
		u.Channels = dedup
		if len(u.Channels) > 32 {
			return fmt.Errorf("user %q: at most 32 channels", u.Name)
		}
	}
	seenDev := map[string]bool{}
	devs := c.Devices[:0]
	for _, d := range c.Devices {
		d.ID = strings.ToLower(strings.TrimSpace(d.ID))
		if d.ID == "" || seenDev[d.ID] {
			continue
		}
		seenDev[d.ID] = true
		if d.UserID != "" && !seenID[d.UserID] {
			d.UserID = ""
		}
		if d.Name == "" {
			d.Name = "Beltpack " + strings.ToUpper(d.ID[max(0, len(d.ID)-4):])
		}
		switch d.Net.Mode {
		case "wifi", "ethernet", "ble":
		default:
			d.Net.Mode = "wifi"
		}
		if !d.Net.DHCP {
			if net.ParseIP(d.Net.IP) == nil || net.ParseIP(d.Net.Mask) == nil {
				d.Net.DHCP = true
			}
		}
		if d.Audio.MicType != "electret" {
			d.Audio.MicType = "dynamic"
		}
		d.Audio.MicGain = clamp(d.Audio.MicGain, 0, 100)
		d.Audio.Sidetone = clamp(d.Audio.Sidetone, 0, 100)
		d.Audio.HPLevel = clamp(d.Audio.HPLevel, 0, 100)
		d.Audio.Bright = clamp(d.Audio.Bright, 0, 100)
		devs = append(devs, d)
	}
	c.Devices = devs
	return nil
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Store persists Config as JSON in the data directory.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

// OpenStore loads (or creates) config.json in dir. If a new config is created
// the generated admin password is returned so it can be shown once.
func OpenStore(dir string) (*Store, string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	s := &Store{path: filepath.Join(dir, "config.json")}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		pass := randHex(5)
		s.cfg = defaultConfig(pass)
		if err := s.cfg.Validate(); err != nil {
			return nil, "", err
		}
		return s, pass, s.saveLocked()
	}
	if err != nil {
		return nil, "", err
	}
	if err := json.Unmarshal(b, &s.cfg); err != nil {
		return nil, "", fmt.Errorf("parse %s: %w", s.path, err)
	}
	if err := s.cfg.Validate(); err != nil {
		return nil, "", fmt.Errorf("%s: %w", s.path, err)
	}
	return s, "", nil
}

func defaultConfig(adminPass string) Config {
	all := func(talk bool, ids ...int) []Access {
		var a []Access
		for _, id := range ids {
			a = append(a, Access{Ch: id, Talk: talk, Listen: true})
		}
		return a
	}
	return Config{
		ServerName:    "OpenLine",
		AdminPassHash: hashPass(adminPass),
		Channels: []Channel{
			{ID: 1, Name: "Production", Color: "#e5484d"},
			{ID: 2, Name: "Camera", Color: "#3e9bf0"},
			{ID: 3, Name: "Audio", Color: "#30a46c"},
			{ID: 4, Name: "Stage", Color: "#f5a524"},
			{ID: 5, Name: "Program", Color: "#8e8c99", ListenOnly: true, Program: true},
			{ID: 6, Name: "Announcers", Color: "#a66ef0"},
		},
		Users: []User{
			{Name: "Director", PIN: "1111", Admin: true, Channels: append(all(true, 1, 6, 2, 3, 4), Access{Ch: 5, Listen: true})},
			{Name: "Camera 1", PIN: "2222", Channels: append(all(true, 2, 1), Access{Ch: 5, Listen: true})},
			{Name: "A1", PIN: "3333", Channels: append(all(true, 3, 1), Access{Ch: 5, Listen: true})},
			{Name: "Program Feed", Bridge: true, Channels: []Access{{Ch: 5, Talk: true}}},
			// NDI link to the announce booth: their talkback in, IFB (this channel) out.
			{Name: "Announcer Link", Bridge: true, Channels: []Access{{Ch: 6, Talk: true, Listen: true}}},
		},
	}
}

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneConfig(s.cfg)
}

// Replace validates and persists a new config. The admin password hash is
// kept unless newPass is non-empty.
func (s *Store) Replace(c Config, newPass string) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.AdminPassHash = s.cfg.AdminPassHash
	c.Devices = s.cfg.Devices // devices are managed through UpdateDevices
	if newPass != "" {
		c.AdminPassHash = hashPass(newPass)
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	s.cfg = cloneConfig(c)
	return cloneConfig(s.cfg), s.saveLocked()
}

// Update applies fn to a copy of the config (including devices) and persists it.
func (s *Store) Update(fn func(c *Config) error) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := cloneConfig(s.cfg)
	if err := fn(&c); err != nil {
		return Config{}, err
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	s.cfg = c
	return cloneConfig(s.cfg), s.saveLocked()
}

func (s *Store) CheckAdmin(pass string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return bcrypt.CompareHashAndPassword([]byte(s.cfg.AdminPassHash), []byte(pass)) == nil
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func cloneConfig(c Config) Config {
	b, _ := json.Marshal(c)
	var out Config
	_ = json.Unmarshal(b, &out)
	return out
}

func hashPass(p string) string {
	h, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return string(h)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
