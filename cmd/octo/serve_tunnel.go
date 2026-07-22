package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"

	"github.com/open-octo/octo-agent/internal/server"
	"github.com/open-octo/octo-agent/internal/tunnel"
)

// defaultRelayURL is octo's hosted relay. A user can point --relay elsewhere to
// self-host the relay or reach a staging one.
const defaultRelayURL = "wss://relay.octo.dev"

// startTunnel brings up the managed-tunnel host bridge for `octo serve --tunnel`.
// It runs in the serve worker: the tunnel is a goroutine in this process and an
// ordinary key-authenticated /ws client of the local server, so internal/server
// is untouched. The goroutine stops when ctx is cancelled (serve shutdown).
func startTunnel(ctx context.Context, srv *server.Server, addr, relayURL string, stdout io.Writer) error {
	idPath, err := tunnelIdentityPath()
	if err != nil {
		return err
	}
	identity, err := tunnel.LoadOrCreateIdentity(idPath)
	if err != nil {
		return err
	}
	token, err := newPairToken()
	if err != nil {
		return err
	}

	tun, err := tunnel.New(tunnel.Config{
		RelayURL:    relayURL,
		TunnelID:    identity.TunnelID(),
		PairTokens:  []string{token},
		LoopbackURL: loopbackWSURL(addr),
		AccessKey:   srv.AccessKey(),
		Identity:    identity,
	})
	if err != nil {
		return err
	}

	// Publish the pairing material so the web UI can render it as a QR, and
	// print it too so a headless server can pair without a browser.
	pairURL := pairingURL(relayURL, identity, token)
	srv.SetTunnelPairing(&server.TunnelPairing{
		PairURL:  pairURL,
		Relay:    relayURL,
		TunnelID: identity.TunnelID(),
	})
	printPairingMaterial(stdout, relayURL, identity, token, pairURL)

	go func() { _ = tun.Serve(ctx) }()
	return nil
}

// pairingURL is the deep link a pairing QR encodes: the four things a phone
// needs to reach and authenticate this host — relay, tunnel id, host public
// key, and the one-time token.
func pairingURL(relayURL string, id *tunnel.Identity, token string) string {
	q := url.Values{
		"relay": {relayURL},
		"tid":   {id.TunnelID()},
		"hk":    {id.PublicKeyBase64()},
		"tok":   {token},
	}
	return "octo-pair://v1?" + q.Encode()
}

// tunnelIdentityPath is ~/.octo/tunnel.json, alongside the other serve state.
func tunnelIdentityPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".octo", "tunnel.json"), nil
}

// newPairToken returns a one-time, single-use pairing token (128 bits of hex).
func newPairToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// loopbackWSURL derives the local /ws URL the tunnel bridges into. A wildcard or
// empty bind host means the server listens on loopback too, so dial 127.0.0.1; a
// specific bind host is dialed as-is (that is the only interface it accepts).
func loopbackWSURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = "127.0.0.1", "8088"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "ws://" + net.JoinHostPort(host, port) + "/ws"
}

// printPairingMaterial shows the raw data a pairing QR encodes. Rendering it as
// a scannable QR (CLI ASCII, web/desktop panel) is a later step; the text is
// enough to pair a device by hand and to test against a relay.
func printPairingMaterial(w io.Writer, relayURL string, id *tunnel.Identity, token, pairURL string) {
	fmt.Fprintln(w, "octo serve: managed tunnel enabled — pair a device with:")
	fmt.Fprintf(w, "  relay:      %s\n", relayURL)
	fmt.Fprintf(w, "  tunnel id:  %s\n", id.TunnelID())
	fmt.Fprintf(w, "  host key:   %s\n", id.PublicKeyBase64())
	fmt.Fprintf(w, "  pair token: %s  (one-time)\n", token)
	fmt.Fprintf(w, "  pair url:   %s\n", pairURL)
	fmt.Fprintln(w, "  (or open Settings › Mobile in the web UI to scan a QR)")
}
