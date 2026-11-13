// Package client is a minimal OpenLine protocol client used by the NDI
// bridge and by tests.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"openline/internal/proto"
)

type Client struct {
	conn *websocket.Conn
	wmu  sync.Mutex

	mu      sync.RWMutex
	welcome proto.Welcome

	// Callbacks (set before Run). Called from the read goroutine.
	OnAudio   func(src, seq uint16, mask uint32, opus []byte)
	OnControl func(t string, raw []byte)
	OnWelcome func(w proto.Welcome)
}

// Dial connects, sends hello and waits for the welcome message.
func Dial(ctx context.Context, url string, hello proto.Msg) (*Client, error) {
	hello.T = "hello"
	hello.Ver = proto.Version
	url = NormalizeURL(url)
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(dctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"User-Agent": {"openline-client"}}})
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(1 << 20)
	cl := &Client{conn: c}
	if err := cl.Send(hello); err != nil {
		c.CloseNow()
		return nil, err
	}
	for {
		typ, data, err := c.Read(dctx)
		if err != nil {
			c.CloseNow()
			return nil, err
		}
		if typ != websocket.MessageText {
			continue
		}
		var m proto.Msg
		json.Unmarshal(data, &m)
		switch m.T {
		case "welcome":
			var w proto.Welcome
			json.Unmarshal(data, &w)
			cl.welcome = w
			return cl, nil
		case "error":
			c.CloseNow()
			return nil, errors.New(m.Msg)
		}
	}
}

// NormalizeURL turns "https://host" or "host" into "wss://host/ws".
func NormalizeURL(u string) string {
	u = strings.TrimSpace(u)
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	case !strings.HasPrefix(u, "ws://") && !strings.HasPrefix(u, "wss://"):
		u = "wss://" + u
	}
	u = strings.TrimRight(u, "/")
	if !strings.HasSuffix(u, "/ws") {
		u += "/ws"
	}
	return u
}

func (c *Client) Welcome() proto.Welcome {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.welcome
}

// ChannelID resolves a channel name (case-insensitive) or numeric id.
// With several shows, "Show B: Production" picks that show's channel.
func (c *Client) ChannelID(name string) (int, error) {
	w := c.Welcome()
	for _, ch := range w.Channels {
		if strings.EqualFold(ch.Name, name) || fmt.Sprint(ch.ID) == name {
			return ch.ID, nil
		}
	}
	if show, chName, ok := strings.Cut(name, ":"); ok {
		for _, ch := range w.Channels {
			if strings.EqualFold(ch.Show, strings.TrimSpace(show)) && strings.EqualFold(ch.Name, strings.TrimSpace(chName)) {
				return ch.ID, nil
			}
		}
	}
	return 0, fmt.Errorf("channel %q not available to user %q", name, w.User)
}

// ChannelIndex returns the mask bit index for a channel id.
func (c *Client) ChannelIndex(id int) int {
	for i, ch := range c.Welcome().Channels {
		if ch.ID == id {
			return i
		}
	}
	return -1
}

func (c *Client) Send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.write(websocket.MessageText, b)
}

func (c *Client) SetTalk(ch int, on bool) error {
	return c.Send(proto.Msg{T: "talk", Ch: ch, On: &on})
}

func (c *Client) SetListen(ch int, on bool) error {
	return c.Send(proto.Msg{T: "listen", Ch: ch, On: &on})
}

func (c *Client) SendAudio(seq uint16, opus []byte) error {
	return c.write(websocket.MessageBinary, proto.EncodeUplink(seq, opus))
}

func (c *Client) write(typ websocket.MessageType, b []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.conn.Write(ctx, typ, b)
}

// Run reads until the connection closes or ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	defer c.conn.CloseNow()
	for {
		typ, data, err := c.conn.Read(ctx)
		if err != nil {
			return err
		}
		if typ == websocket.MessageBinary {
			if src, seq, mask, opus, err := proto.DecodeDownlink(data); err == nil && c.OnAudio != nil {
				c.OnAudio(src, seq, mask, opus)
			}
			continue
		}
		var m proto.Msg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m.T == "welcome" {
			var w proto.Welcome
			json.Unmarshal(data, &w)
			c.mu.Lock()
			c.welcome = w
			c.mu.Unlock()
			if c.OnWelcome != nil {
				c.OnWelcome(w)
			}
		}
		if c.OnControl != nil {
			c.OnControl(m.T, data)
		}
	}
}

func (c *Client) Close() { c.conn.Close(websocket.StatusNormalClosure, "bye") }
