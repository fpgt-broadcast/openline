// Command openline-bridge runs on an on-site computer and connects NDI audio
// (program feed, announcer talkback) to an OpenLine server, and publishes
// intercom channels back out as NDI sources (e.g. IFB to the announcers).
//
//	openline-bridge -list                 # show NDI sources on the network
//	openline-bridge -devices              # show sound card outputs
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
	"openline/internal/portaudio"
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
  ],
  "vmix": { "host": "192.168.1.20", "port": 8099, "token": "PASTE token of any NDI-bridge user (e.g. Program Feed)" },
  "outputs": {
    "token": "PASTE token of a user with the 'Channel outputs' role",
    "ndiPrefix": "OpenLine",
    "card": { "device": "", "latencyMs": 60 }
  }
}
`

func main() {
	cfgPath := flag.String("config", "bridge.json", "config file")
	list := flag.Bool("list", false, "list NDI sources and exit")
	initCfg := flag.Bool("init", false, "print an example config and exit")
	extra := flag.String("extra-ips", "", "comma separated IPs to probe for NDI sources (with -list)")
	devices := flag.Bool("devices", false, "list sound card outputs and exit")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	switch {
	case *showVersion:
		fmt.Println(version)
		return
	case *initCfg:
		fmt.Print(example)
		return
	case *devices:
		for _, input := range []bool{false, true} {
			list, title := portaudio.Outputs, "Sound card outputs"
			if input {
				list, title = portaudio.Inputs, "Sound card inputs"
			}
			devs, err := list()
			if err != nil {
				log.Fatal(err)
			}
			fmt.Println(title + ":")
			if len(devs) == 0 {
				fmt.Println("  none found")
			}
			for _, d := range devs {
				fmt.Println("  " + d.String())
			}
			fmt.Println()
		}
		fmt.Println(`Use a name (or part of it) as "device": in "outputs" > "card", or in a link's "in"/"out" with "type": "card".`)
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
	if len(cfg.Links) == 0 && cfg.VMix == nil && cfg.Outputs == nil {
		log.Fatal("config has no links, outputs or vmix section")
	}
	if len(cfg.Links) > 0 || cfg.Outputs != nil {
		if err := opus.Load(); err != nil {
			log.Fatal(err)
		}
	}
	needNDI := false
	for _, l := range cfg.Links {
		if (l.In != nil && (l.In.Type == "" || l.In.Type == "ndi")) || (l.Out != nil && (l.Out.Type == "" || l.Out.Type == "ndi")) {
			needNDI = true
		}
	}
	if needNDI {
		if err := ndi.Load(); err != nil {
			if cfg.VMix == nil {
				log.Fatal(err)
			}
			// Keep vMix tally running even when NDI isn't installed yet.
			log.Printf("WARNING: %v\nNDI links will keep retrying; vMix tally still runs.", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	lg := log.New(os.Stderr, "", log.LstdFlags)
	lg.Printf("OpenLine bridge %s → %s (%d links%s)", version, cfg.Server, len(cfg.Links), map[bool]string{true: ", channel outputs", false: ""}[cfg.Outputs != nil])
	var wg sync.WaitGroup
	if cfg.VMix != nil {
		wg.Add(1)
		go func() { defer wg.Done(); bridge.RunVMixTally(ctx, cfg.Server, *cfg.VMix, lg) }()
	}
	if cfg.Outputs != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bridge.NewOutputs(*cfg.Outputs, cfg.Server, bridge.DefaultOpener, lg).Run(ctx)
		}()
	}
	for _, lc := range cfg.Links {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bridge.NewLink(lc, cfg.Server, cfg.ExtraIPs, bridge.DefaultOpener, lg).Run(ctx)
		}()
	}
	wg.Wait()
}
