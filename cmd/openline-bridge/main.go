// Command openline-bridge runs on an on-site computer and connects NDI audio
// (program feed, announcer talkback) to an OpenLine server, and publishes
// intercom channels back out as NDI sources (e.g. IFB to the announcers).
//
//	openline-bridge -list                 # show NDI sources on the network
//	openline-bridge -init > bridge.json   # write an example config
//	openline-bridge -config bridge.json   # run
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"openline/internal/bridge"
	"openline/internal/ndi"
	"openline/internal/opus"
)

var version = "dev"

const example = `{
  "server": "https://intercom.example.com",
  "ndiExtraIPs": "",
  "links": [
    {
      "name": "Program",
      "token": "PASTE token of the 'Program Feed' user (Admin > Users > Edit)",
      "talk": ["Program"],
      "in": { "type": "ndi", "source": "PRODUCTION-PC (Program)", "channels": [0, 1], "music": true }
    },
    {
      "name": "Announcers",
      "token": "PASTE token of an 'Announcer Link' user (NDI bridge role, Talk+Listen on Announcers)",
      "talk": ["Announcers"],
      "listen": ["Announcers"],
      "in":  { "type": "ndi", "source": "BOOTH-PC (Talkback)", "channels": [0], "gateDb": -45 },
      "out": { "type": "ndi", "name": "OpenLine IFB Announcers" }
    }
  ]
}
`

func main() {
	cfgPath := flag.String("config", "bridge.json", "config file")
	list := flag.Bool("list", false, "list NDI sources and exit")
	initCfg := flag.Bool("init", false, "print an example config and exit")
	extra := flag.String("extra-ips", "", "comma separated IPs to probe for NDI sources (with -list)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	switch {
	case *showVersion:
		fmt.Println(version)
		return
	case *initCfg:
		fmt.Print(example)
		return
	case *list:
		srcs, err := ndi.FindSources(4*time.Second, *extra)
		if err != nil {
			log.Fatal(err)
		}
		if len(srcs) == 0 {
			fmt.Println("no NDI sources found")
		}
		for _, s := range srcs {
			fmt.Printf("%-50s %s\n", s.Name, s.URL)
		}
		return
	}

	cfg, err := bridge.LoadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("%v\n(create one with: openline-bridge -init > bridge.json)", err)
	}
	if err := opus.Load(); err != nil {
		log.Fatal(err)
	}
	needNDI := false
	for _, l := range cfg.Links {
		if (l.In != nil && (l.In.Type == "" || l.In.Type == "ndi")) || (l.Out != nil && (l.Out.Type == "" || l.Out.Type == "ndi")) {
			needNDI = true
		}
	}
	if needNDI {
		if err := ndi.Load(); err != nil {
			log.Fatal(err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	lg := log.New(os.Stderr, "", log.LstdFlags)
	lg.Printf("OpenLine bridge %s → %s (%d links)", version, cfg.Server, len(cfg.Links))
	var wg sync.WaitGroup
	for _, lc := range cfg.Links {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bridge.NewLink(lc, cfg.Server, cfg.ExtraIPs, bridge.DefaultOpener, lg).Run(ctx)
		}()
	}
	wg.Wait()
}
