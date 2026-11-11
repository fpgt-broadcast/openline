package bridge

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"openline/internal/client"
	"openline/internal/proto"
)

// VMixConfig enables tally from vMix's TCP API (Settings > Web Controller is
// not needed; the TCP API on port 8099 is always on).
type VMixConfig struct {
	Host  string `json:"host"`  // vMix PC, e.g. "192.168.1.20"
	Port  int    `json:"port"`  // default 8099
	Token string `json:"token"` // token of a user with the "NDI bridge" role
}

// ParseVMixTally parses "TALLY OK 0121": one digit per input, starting at
// input 1; 1 = program (live), 2 = preview.
func ParseVMixTally(line string) (pgm, pvw []int, ok bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "TALLY OK ") {
		return nil, nil, false
	}
	pgm, pvw = []int{}, []int{}
	for i, c := range strings.TrimSpace(line[len("TALLY OK "):]) {
		switch c {
		case '1':
			pgm = append(pgm, i+1)
		case '2':
			pvw = append(pvw, i+1)
		}
	}
	return pgm, pvw, true
}

// RunVMixTally reads tally from vMix and forwards it to the server until ctx
// ends, reconnecting to both as needed. If vMix is unreachable for more than
// a few seconds, tally is cleared so nobody sees a stale LIVE light.
func RunVMixTally(ctx context.Context, server string, vc VMixConfig, lg *log.Logger) {
	if vc.Port == 0 {
		vc.Port = 8099
	}
	latest := make(chan proto.TallyUpdate, 1)
	publish := func(u proto.TallyUpdate) {
		select {
		case <-latest:
		default:
		}
		latest <- u
	}
	go tallyServerLoop(ctx, server, vc.Token, latest, lg)

	addr := net.JoinHostPort(vc.Host, fmt.Sprint(vc.Port))
	backoff := time.Second
	var lostAt time.Time
	cleared := true
	for ctx.Err() == nil {
		conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", addr)
		if err != nil {
			if lostAt.IsZero() {
				lostAt = time.Now()
				lg.Printf("[vmix] cannot reach %s: %v", addr, err)
			}
			if !cleared && time.Since(lostAt) > 5*time.Second {
				publish(proto.TallyUpdate{T: "tally", Program: []int{}, Preview: []int{}})
				cleared = true
			}
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff = min(backoff*2, 10*time.Second)
			continue
		}
		backoff, lostAt = time.Second, time.Time{}
		lg.Printf("[vmix] connected to %s", addr)
		go func() { <-ctx.Done(); conn.Close() }()
		// Subscribe to changes, and ask for the current state right away
		// (vMix only pushes on the next change otherwise).
		fmt.Fprintf(conn, "SUBSCRIBE TALLY\r\nTALLY\r\n")
		sc := bufio.NewScanner(conn)
		lastLine := ""
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if pgm, pvw, ok := ParseVMixTally(line); ok {
				if line != lastLine {
					lg.Printf("[vmix] tally: on air %v, preview %v", pgm, pvw)
					lastLine = line
				}
				publish(proto.TallyUpdate{T: "tally", Program: pgm, Preview: pvw})
				cleared = false
			} else if strings.Contains(line, " ER") {
				lg.Printf("[vmix] vMix replied: %s", line)
			}
		}
		conn.Close()
		if ctx.Err() == nil {
			lg.Printf("[vmix] disconnected from %s", addr)
			lostAt = time.Now()
		}
	}
}

func tallyServerLoop(ctx context.Context, server, token string, latest <-chan proto.TallyUpdate, lg *log.Logger) {
	var last *proto.TallyUpdate
	for ctx.Err() == nil {
		cl, err := client.Dial(ctx, server, proto.Msg{Token: token, Client: "bridge-tally"})
		if err != nil {
			lg.Printf("[vmix] cannot send tally to the server: %v (check \"server\" and the vmix \"token\" in bridge.json)", err)
			select {
			case <-time.After(3 * time.Second):
				continue
			case <-ctx.Done():
				return
			}
		}
		lg.Printf("[vmix] sending tally to the server as user %q", cl.Welcome().User)
		rctx, cancel := context.WithCancel(ctx)
		cl.OnControl = func(t string, raw []byte) {
			if t == "error" {
				lg.Printf("[vmix] server says: %s", raw)
			}
		}
		go func() { cl.Run(rctx); cancel() }()
		if last != nil {
			cl.Send(*last)
		}
	loop:
		for {
			select {
			case u := <-latest:
				last = &u
				if cl.Send(u) != nil {
					break loop
				}
			case <-rctx.Done():
				break loop
			}
		}
		cancel()
		cl.Close()
	}
}
