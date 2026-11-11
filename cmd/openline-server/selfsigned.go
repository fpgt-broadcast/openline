package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// selfSignedCert returns a certificate for this machine's LAN addresses,
// stored in dataDir and regenerated when the addresses change or it nears
// expiry. Browsers show a one-time warning; once accepted, the page counts as
// a secure context, so microphones and audio work on the local network
// without internet access or a domain.
func selfSignedCert(dataDir string) (tls.Certificate, error) {
	certPath := filepath.Join(dataDir, "selfsigned.crt")
	keyPath := filepath.Join(dataDir, "selfsigned.key")

	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	for _, s := range lanIPs() {
		if ip := net.ParseIP(s); ip != nil {
			ips = append(ips, ip)
		}
	}
	host, _ := os.Hostname()
	names := []string{"localhost"}
	if host != "" {
		names = append(names, host, host+".local")
	}

	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		if leaf, err := x509.ParseCertificate(c.Certificate[0]); err == nil &&
			time.Until(leaf.NotAfter) > 30*24*time.Hour && coversIPs(leaf, ips) {
			return c, nil
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "OpenLine intercom (self-signed)", Organization: []string{"OpenLine"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(397 * 24 * time.Hour), // Apple caps TLS server certs at 398 days
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           ips,
		DNSNames:              names,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	log.Printf("generated self-signed certificate for %v %v", names, ips)
	return tls.X509KeyPair(certPEM, keyPEM)
}

func coversIPs(c *x509.Certificate, ips []net.IP) bool {
	for _, ip := range ips {
		if !slices.ContainsFunc(c.IPAddresses, ip.Equal) {
			return false
		}
	}
	return true
}
