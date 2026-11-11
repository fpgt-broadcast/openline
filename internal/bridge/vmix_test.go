package bridge

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"openline/internal/client"
	"openline/internal/proto"
	"openline/internal/server"
)

func TestParseVMixTally(t *testing.T) {
	pgm, pvw, ok := ParseVMixTally("TALLY OK 0121\r\n")
	if !ok || fmt.Sprint(pgm) != "[2 4]" || fmt.Sprint(pvw) != "[3]" {
		t.Fatalf("got %v %v %v", pgm, pvw, ok)
	}
	if _, _, ok := ParseVMixTally("SUBSCRIBE OK TALLY"); ok {
		t.Fatal("non-tally line parsed")
	}
}

// fakeVMix accepts one connection, expects SUBSCRIBE TALLY and sends lines.
func fakeVMix(t *testing.T, lines chan string) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		cmd, _ := r.ReadString('\n')
		if strings.TrimSpace(cmd) != "SUBSCRIBE TALLY" {
			t.Errorf("unexpected command %q", cmd)
			return
		}
		fmt.Fprint(c, "SUBSCRIBE OK TALLY\r\n")
		for l := range lines {
			fmt.Fprint(c, l+"\r\n")
		}
	}()
	return ln.Addr().String()
}

func TestVMixTallyReachesUsers(t *testing.T) {
	store, _, err := server.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.Update(func(c *server.Config) error {
		for i := range c.Users {
			if c.Users[i].Name == "Camera 1" {
				c.Users[i].Tally = []int{2}
			}
		}
		return nil
	})
	ts := httptest.NewServer(server.New(store, fstest.MapFS{}).Handler())
	defer ts.Close()
	var bridgeTok string
	for _, u := range store.Get().Users {
		if u.Name == "Program Feed" {
			bridgeTok = u.Token
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cam, err := client.Dial(ctx, ts.URL, proto.Msg{User: "Camera 1", PIN: "2222"})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var states []string
	cam.OnControl = func(typ string, raw []byte) {
		if typ == "tally" {
			mu.Lock()
			states = append(states, string(raw))
			mu.Unlock()
		}
	}
	go cam.Run(ctx)

	lines := make(chan string, 4)
	host, port, _ := net.SplitHostPort(fakeVMix(t, lines))
	var p int
	fmt.Sscan(port, &p)
	go RunVMixTally(ctx, ts.URL, VMixConfig{Host: host, Port: p, Token: bridgeTok}, quietLog())

	wait := func(want string) {
		t.Helper()
		for i := 0; i < 100; i++ {
			mu.Lock()
			n := len(states)
			last := ""
			if n > 0 {
				last = states[n-1]
			}
			mu.Unlock()
			if strings.Contains(last, want) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("waiting for %s, got %v", want, states)
	}
	lines <- "TALLY OK 0120" // input 2 = program
	wait(`"state":"program"`)
	lines <- "TALLY OK 1200" // input 2 = preview
	wait(`"state":"preview"`)
	lines <- "TALLY OK 1000"
	wait(`"state":"off"`)

	// A non-bridge user cannot fake tally.
	cam.Send(proto.TallyUpdate{T: "tally", Program: []int{2}})
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	last := states[len(states)-1]
	mu.Unlock()
	if !strings.Contains(last, `"off"`) {
		t.Fatalf("non-bridge tally accepted: %s", last)
	}
}
