package tunnel

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	initialBackoff = 500 * time.Millisecond
	maxBackoff     = 30 * time.Second
)

// tunnelLabelRe is the shape of a tunnel id that can ride in a DNS label
// under a single-level wildcard cert: lowercase letters, digits, hyphens,
// at most 63 chars. Production ids are 32-char lowercase hex; anything else
// (a hand-edited tunnel.json) falls back to query-only routing rather than
// producing an undialable hostname.
var tunnelLabelRe = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)

// relayDialBase returns the relay base URL to dial for a tunnel. Against a
// DNS-named relay it prefixes the tunnel id as a subdomain
// (wss://<tunnelid>.relay.octo.dev) so a multi-node deployment's L4 balancer
// can consistent-hash the TLS SNI and land both ends of a tunnel on the same
// node without decrypting anything. IP literals and dotless hosts (localhost,
// the PoC's 127.0.0.1 test relays) can't carry a subdomain, so they dial
// unchanged. The ?tunnel= query is always sent too, so a new client keeps
// working against a single-node relay that only reads the query.
//
// The eligibility rule (dotted host, not an IP — including trailing-dot and
// dots-and-digits shapes) is mirrored by the mobile natives' deviceUrl/
// deviceURL; keep the three identical, or the two ends of a tunnel hash to
// different nodes.
func relayDialBase(relayURL, tunnelID string) string {
	if !tunnelLabelRe.MatchString(tunnelID) {
		return relayURL
	}
	u, err := url.Parse(relayURL)
	if err != nil {
		return relayURL
	}
	host := u.Hostname()
	if host == "" || !strings.Contains(host, ".") {
		return relayURL
	}
	trimmed := strings.TrimSuffix(host, ".")
	if net.ParseIP(trimmed) != nil || digitsAndDotsOnly(trimmed) {
		return relayURL
	}
	if port := u.Port(); port != "" {
		u.Host = net.JoinHostPort(tunnelID+"."+host, port)
	} else {
		u.Host = tunnelID + "." + host
	}
	return u.String()
}

// digitsAndDotsOnly matches the mobile natives' cheap IP-ish test so shapes
// like "1.2.3.4.5" (not a valid IP, but clearly not a name either) are
// treated identically on every end.
func digitsAndDotsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

// Config configures a host tunnel. Zero LoopbackURL/RelayURL/TunnelID is a
// programming error; the rest have sensible defaults.
type Config struct {
	// RelayURL is the relay base, e.g. wss://<tunnelid>.relay.octo.dev. The PoC
	// and tests use ws://127.0.0.1:<port>.
	RelayURL string
	// TunnelID is this host's tunnel identity on the relay.
	TunnelID string
	// PairTokens are the one-time pairing tokens the host offers (one per phone
	// it expects to pair). Production mints these per QR.
	PairTokens []string
	// LoopbackURL points at the local server's /ws, e.g. ws://127.0.0.1:8088/ws.
	// Its host:port is the base the bridge dials for both loopback HTTP (/api)
	// and loopback WebSocket (/ws) calls.
	LoopbackURL string
	// AccessKey would authenticate a non-loopback client. The bridge dials
	// 127.0.0.1, which the server exempts from the key check, so it is unused for
	// now; kept for a future non-loopback bind.
	AccessKey string
	// Identity supplies the host's Noise static keypair (and, if TunnelID is
	// unset, the tunnel id). Load it with LoadOrCreateIdentity to persist it in
	// ~/.octo. When nil, New generates a throwaway identity — the PoC/test path.
	Identity *Identity
	// Logf overrides the logger. nil uses log.Printf.
	Logf func(string, ...any)
}

// Tunnel is the host side of the managed tunnel: one relay connection bridging N
// paired devices to the local server, each over its own Noise session.
type Tunnel struct {
	cfg  Config
	logf func(string, ...any)

	httpBase   string // http(s)://host:port — loopback base for /api replays
	wsBase     string // ws(s)://host:port     — loopback base for /ws streams
	httpClient *http.Client

	relayMu sync.Mutex // guards the relay pointer and serializes writes to it
	relay   *websocket.Conn

	mu      sync.Mutex
	devices map[string]*device

	// pushRegs holds per-phone push-token registrations, keyed by the phone's
	// base64 Noise static key (stable across reconnects). Guarded by pushMu;
	// see wakeup.go.
	pushMu   sync.Mutex
	pushRegs map[string]*pushReg
}

// device is one paired phone: its Noise session and, once the handshake
// completes, the bridge that demultiplexes its shim frames onto the loopback
// server.
type device struct {
	id   string
	sess *session
	br   *bridge
}

// New builds a tunnel. If cfg.Identity is nil, a throwaway one is generated
// (PoC/test path); production passes a persisted identity.
func New(cfg Config) (*Tunnel, error) {
	if cfg.Identity == nil {
		kp, err := generateKeypair()
		if err != nil {
			return nil, err
		}
		cfg.Identity = &Identity{tunnelID: cfg.TunnelID, static: kp}
	}
	if cfg.TunnelID == "" {
		cfg.TunnelID = cfg.Identity.tunnelID
	}
	if cfg.RelayURL == "" || cfg.TunnelID == "" || cfg.LoopbackURL == "" {
		return nil, fmt.Errorf("tunnel: RelayURL, TunnelID and LoopbackURL are required")
	}
	httpBase, wsBase, err := loopbackBases(cfg.LoopbackURL)
	if err != nil {
		return nil, err
	}
	logf := cfg.Logf
	if logf == nil {
		logf = log.Printf
	}
	return &Tunnel{
		cfg:        cfg,
		logf:       logf,
		httpBase:   httpBase,
		wsBase:     wsBase,
		httpClient: &http.Client{},
		devices:    map[string]*device{},
	}, nil
}

// loopbackBases derives the HTTP and WebSocket loopback bases from the /ws URL.
func loopbackBases(loopbackURL string) (httpBase, wsBase string, err error) {
	u, err := url.Parse(loopbackURL)
	if err != nil {
		return "", "", fmt.Errorf("tunnel: parse LoopbackURL: %w", err)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("tunnel: LoopbackURL has no host")
	}
	if u.Scheme == "wss" || u.Scheme == "https" {
		return "https://" + u.Host, "wss://" + u.Host, nil
	}
	return "http://" + u.Host, "ws://" + u.Host, nil
}

// Serve maintains the relay connection with reconnect/backoff until ctx is
// cancelled — the same infinite-retry posture the editor plugins use against
// serve. Each connection lifecycle is one runOnce.
func (t *Tunnel) Serve(ctx context.Context) error {
	// Watch the server's global activity broadcasts for push wakeups (M1c).
	go t.watchActivity(ctx)
	backoff := initialBackoff
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := t.runOnce(ctx)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		t.logf("[tunnel] relay connection ended (%v); reconnecting in %s", err, backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// runOnce dials the relay and bridges until the connection drops or ctx is
// cancelled. Every device's bridge is torn down on return.
func (t *Tunnel) runOnce(ctx context.Context) error {
	q := url.Values{"tunnel": {t.cfg.TunnelID}}
	for _, tok := range t.cfg.PairTokens {
		q.Add("pairtoken", tok)
	}
	relayURL := relayDialBase(t.cfg.RelayURL, t.cfg.TunnelID) + "/host?" + q.Encode()

	ws, _, err := websocket.DefaultDialer.DialContext(ctx, relayURL, nil)
	if err != nil {
		return fmt.Errorf("dial relay: %w", err)
	}

	t.relayMu.Lock()
	t.relay = ws
	t.relayMu.Unlock()
	t.mu.Lock()
	t.devices = map[string]*device{}
	t.mu.Unlock()
	t.logf("[tunnel] connected to relay tunnel=%s", t.cfg.TunnelID)

	// When the read loop exits, stop the relay socket and tear down every
	// device's bridge (which closes its loopback streams).
	defer func() {
		ws.Close()
		t.relayMu.Lock()
		t.relay = nil
		t.relayMu.Unlock()
		t.mu.Lock()
		for _, d := range t.devices {
			if d.br != nil {
				d.br.close()
			}
		}
		t.devices = map[string]*device{}
		t.mu.Unlock()
	}()

	// Cancel the read by closing the socket when ctx ends.
	go func() {
		<-ctx.Done()
		ws.Close()
	}()

	for {
		f, err := readFrame(ws)
		if err != nil {
			return err
		}
		if err := t.handleRelayFrame(ctx, f); err != nil {
			t.logf("[tunnel] device=%s: %v", f.Device, err)
		}
	}
}

func (t *Tunnel) handleRelayFrame(ctx context.Context, f frame) error {
	switch f.Type {
	case frameDeviceJoined:
		s, err := newSession(false /* responder */, t.cfg.Identity.static)
		if err != nil {
			return err
		}
		t.setDevice(&device{id: f.Device, sess: s})
		return nil

	case frameHandshake:
		d := t.device(f.Device)
		if d == nil {
			return fmt.Errorf("handshake for unknown device")
		}
		if _, err := d.sess.read(f.Payload); err != nil {
			return err
		}
		if !d.sess.done {
			msg, err := d.sess.write(nil)
			if err != nil {
				return err
			}
			return t.writeRelay(frame{Type: frameHandshake, Device: f.Device, Payload: msg})
		}
		// Handshake complete: stand up the device's bridge. Loopback connections
		// open lazily, per shim frame, so there is nothing to dial here.
		d.br = newBridge(ctx, t, f.Device, d.sess)
		return nil

	case frameData:
		d := t.device(f.Device)
		if d == nil || !d.sess.done || d.br == nil {
			return fmt.Errorf("data with no ready session")
		}
		plaintext, err := d.sess.decrypt(f.Payload)
		if err != nil {
			return err
		}
		sf, err := decodeShimFrame(plaintext)
		if err != nil {
			return fmt.Errorf("decode shim frame: %w", err)
		}
		d.br.handle(sf)
		return nil

	default:
		return nil
	}
}

func (t *Tunnel) writeRelay(f frame) error {
	t.relayMu.Lock()
	defer t.relayMu.Unlock()
	if t.relay == nil {
		return fmt.Errorf("relay not connected")
	}
	return writeFrame(t.relay, f)
}

func (t *Tunnel) device(id string) *device {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.devices[id]
}

func (t *Tunnel) setDevice(d *device) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.devices[d.id] = d
}
