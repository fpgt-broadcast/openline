// Command openline-server is the self-hosted OpenLine intercom server.
//
//	openline-server                         # HTTP on :8999 (use behind a tunnel / reverse proxy)
//	openline-server -domain intercom.example.com   # automatic HTTPS via Let's Encrypt on :443
//	openline-server -cert fullchain.pem -key privkey.pem -listen :8443
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/crypto/acme/autocert"

	"openline/internal/server"
	"openline/web"
)

var version = "dev"

func main() {
	dataDir := flag.String("data", "./data", "directory for config.json and TLS cache")
	listen := flag.String("listen", ":8999", "listen address (ignored with -domain)")
	domain := flag.String("domain", "", "public domain name: enables automatic HTTPS (Let's Encrypt) on :443 and :80")
	email := flag.String("email", "", "contact email for Let's Encrypt (optional)")
	certFile := flag.String("cert", "", "TLS certificate file (PEM)")
	keyFile := flag.String("key", "", "TLS key file (PEM)")
	selfSigned := flag.Bool("self-signed", false, "serve HTTPS with an auto-generated certificate (LAN use without internet/domain; browsers warn once)")
	resetAdmin := flag.String("reset-admin", "", "set a new admin password and exit")
	trustedProxy := flag.String("trusted-proxy", "", "reverse proxies allowed to pass the client IP (CF-Connecting-IP / X-Forwarded-For), comma-separated IPs, CIDRs or host names, e.g. cloudflared. Loopback is always trusted")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	store, genPass, err := server.OpenStore(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	if *resetAdmin != "" {
		if _, err := store.Replace(store.Get(), *resetAdmin); err != nil {
			log.Fatal(err)
		}
		fmt.Println("admin password updated")
		return
	}
	if *domain != "" {
		cfg := store.Get()
		if cfg.PublicURL == "" {
			cfg.PublicURL = "https://" + *domain
			store.Replace(cfg, "")
		}
	}

	srv := server.New(store, web.FS())
	if *trustedProxy != "" {
		srv.TrustProxies(strings.Split(*trustedProxy, ","))
	}
	h := srv.Handler()

	banner(store, genPass, *listen, *domain, *certFile != "" || *selfSigned)
	if *selfSigned && *domain == "" && *certFile == "" {
		fmt.Println(" Self-signed HTTPS: on each device, accept the certificate warning once")
		fmt.Println(" (iPhone Safari: Show Details > visit this website; Chrome: Advanced > Proceed).")
	}

	switch {
	case *domain != "":
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(*domain),
			Cache:      autocert.DirCache(filepath.Join(*dataDir, "certs")),
			Email:      *email,
		}
		go func() {
			// :80 serves ACME challenges and redirects everything else.
			log.Fatal(http.ListenAndServe(":80", m.HTTPHandler(nil)))
		}()
		hs := &http.Server{
			Addr:              ":443",
			Handler:           h,
			TLSConfig:         &tls.Config{GetCertificate: m.GetCertificate, NextProtos: []string{"h2", "http/1.1", "acme-tls/1"}},
			ReadHeaderTimeout: 10 * time.Second,
		}
		log.Fatal(hs.ListenAndServeTLS("", ""))
	case *certFile != "":
		hs := &http.Server{Addr: *listen, Handler: h, ReadHeaderTimeout: 10 * time.Second}
		log.Fatal(hs.ListenAndServeTLS(*certFile, *keyFile))
	case *selfSigned:
		cert, err := selfSignedCert(*dataDir)
		if err != nil {
			log.Fatal(err)
		}
		hs := &http.Server{Addr: *listen, Handler: h, ReadHeaderTimeout: 10 * time.Second,
			TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}}}
		log.Fatal(hs.ListenAndServeTLS("", ""))
	default:
		hs := &http.Server{Addr: *listen, Handler: h, ReadHeaderTimeout: 10 * time.Second}
		log.Fatal(hs.ListenAndServe())
	}
}

func banner(store *server.Store, genPass, listen, domain string, tlsOn bool) {
	cfg := store.Get()
	fmt.Println("────────────────────────────────────────────")
	fmt.Printf(" OpenLine intercom server %s\n", version)
	fmt.Println("────────────────────────────────────────────")
	if genPass != "" {
		fmt.Printf(" First run: admin password is  %s\n", genPass)
		fmt.Println(" (change it on the Admin page; shown only once)")
		fmt.Println(" Demo users: Director/1111  Camera 1/2222  A1/3333")
	}
	var urls []string
	if cfg.PublicURL != "" {
		urls = append(urls, cfg.PublicURL)
	}
	if domain == "" {
		scheme := "http"
		if tlsOn {
			scheme = "https"
		}
		host, port, _ := net.SplitHostPort(listen)
		hosts := lanIPs()
		if host != "" && host != "0.0.0.0" && host != "::" {
			hosts = []string{host}
		}
		for _, ip := range hosts {
			urls = append(urls, fmt.Sprintf("%s://%s:%s", scheme, ip, port))
		}
		fmt.Printf(" Listening on %s\n", listen)
		if !tlsOn {
			fmt.Println(" NOTE: browsers only allow the microphone over HTTPS (or localhost).")
			fmt.Println("       Off-site: use a tunnel or -domain. On the LAN: add -self-signed. See docs/DEPLOY.md")
		}
	}
	for _, u := range urls {
		fmt.Printf(" Open: %s\n", u)
	}
	if len(urls) > 0 {
		if q, err := qrcode.New(urls[0], qrcode.Low); err == nil {
			fmt.Print(q.ToSmallString(false))
		}
	}
	fmt.Println("────────────────────────────────────────────")
	os.Stdout.Sync()
}

func lanIPs() []string {
	var out []string
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || strings.HasPrefix(ipn.IP.String(), "169.254") {
				continue
			}
			out = append(out, ipn.IP.String())
		}
	}
	if len(out) == 0 {
		out = []string{"localhost"}
	}
	return out
}
