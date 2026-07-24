package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/cmd/octo-relay/internal/relay"
)

// TestServeRejectsHalfTLSConfig: one of --tls-cert/--tls-key without the other
// must be an error, not a silent plaintext fallback.
func TestServeRejectsHalfTLSConfig(t *testing.T) {
	for _, tc := range [][2]string{{"cert.pem", ""}, {"", "key.pem"}} {
		if err := serve("127.0.0.1:0", tc[0], tc[1], http.NotFoundHandler()); err == nil {
			t.Errorf("serve(cert=%q, key=%q) = nil, want error", tc[0], tc[1])
		}
	}
}

// TestHealthzPlaintext: /healthz responds 200 with the version, and the relay
// endpoints stay mounted beside it.
func TestHealthzPlaintext(t *testing.T) {
	srv := httptest.NewServer(withHealthz(relay.New().Handler(), "1.2.3-test"))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := string(body); !strings.Contains(got, "1.2.3-test") {
		t.Errorf("body = %q, want version in it", got)
	}

	// The relay endpoints must still answer through the wrapped handler:
	// /host without a tunnel id is its documented 400.
	res2, err := http.Get(srv.URL + "/host")
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusBadRequest {
		t.Errorf("/host status = %d, want 400", res2.StatusCode)
	}
}

// TestServeTLS: with a cert/key pair, serve terminates TLS and /healthz
// answers over it — the code path a hosted relay actually runs.
func TestServeTLS(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeSelfSigned(t, dir)

	// Grab a free port first: serve blocks, so run it in a goroutine and poll.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	errc := make(chan error, 1)
	go func() { errc <- serve(addr, certFile, keyFile, withHealthz(relay.New().Handler(), "tls-test")) }()

	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		Timeout:   2 * time.Second,
	}
	var lastErr error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		select {
		case err := <-errc:
			t.Fatalf("serve exited: %v", err)
		default:
		}
		res, err := client.Get("https://" + addr + "/healthz")
		if err == nil {
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "tls-test") {
				t.Fatalf("healthz over TLS: status=%d body=%q", res.StatusCode, body)
			}
			return
		}
		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("healthz over TLS never came up: %v", lastErr)
}

// TestServeTLSBadCert: an unloadable cert must fail startup loudly.
func TestServeTLSBadCert(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(bad, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := serve("127.0.0.1:0", bad, bad, http.NotFoundHandler()); err == nil {
		t.Fatal("serve with a garbage cert = nil, want error")
	}
}

// writeSelfSigned mints a throwaway self-signed cert for 127.0.0.1 and writes
// the PEM pair into dir.
func writeSelfSigned(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "octo-relay-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}
