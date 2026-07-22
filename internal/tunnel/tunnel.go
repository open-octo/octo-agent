package tunnel

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	initialBackoff = 500 * time.Millisecond
	maxBackoff     = 30 * time.Second
)

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
	// LoopbackURL is the local server's /ws, e.g. ws://127.0.0.1:8088/ws.
	LoopbackURL string
	// AccessKey is presented to the local /ws as ?access_key=. Loopback is
	// exempt from the key check, so this is belt-and-suspenders, but it keeps
	// the tunnel an ordinary key-authenticated client.
	AccessKey string
	// Identity supplies the host's Noise static keypair (and, if TunnelID is
	// unset, the tunnel id). Load it with LoadOrCreateIdentity to persist it in
	// ~/.octo. When nil, New generates a throwaway identity — the PoC/test path.
	Identity *Identity
	// Logf overrides the logger. nil uses log.Printf.
	Logf func(string, ...any)
}

// Tunnel is the host side of the managed tunnel: one relay connection bridging N
// paired devices to the local /ws, each over its own Noise session.
type Tunnel struct {
	cfg  Config
	logf func(string, ...any)

	relayMu sync.Mutex // guards the relay pointer and serializes writes to it
	relay   *websocket.Conn

	mu      sync.Mutex
	devices map[string]*device
}

// device is one paired phone: its Noise session and its dedicated loopback /ws
// connection to the local server.
type device struct {
	id       string
	sess     *session
	loopback *websocket.Conn
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
	logf := cfg.Logf
	if logf == nil {
		logf = log.Printf
	}
	return &Tunnel{cfg: cfg, logf: logf, devices: map[string]*device{}}, nil
}

// Serve maintains the relay connection with reconnect/backoff until ctx is
// cancelled — the same infinite-retry posture the editor plugins use against
// serve. Each connection lifecycle is one runOnce.
func (t *Tunnel) Serve(ctx context.Context) error {
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
// cancelled. All per-device loopback connections are torn down on return.
func (t *Tunnel) runOnce(ctx context.Context) error {
	q := url.Values{"tunnel": {t.cfg.TunnelID}}
	for _, tok := range t.cfg.PairTokens {
		q.Add("pairtoken", tok)
	}
	relayURL := t.cfg.RelayURL + "/host?" + q.Encode()

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

	// When the read loop exits, stop the relay socket and every loopback socket
	// so their pump goroutines unblock and return.
	defer func() {
		ws.Close()
		t.relayMu.Lock()
		t.relay = nil
		t.relayMu.Unlock()
		t.mu.Lock()
		for _, d := range t.devices {
			if d.loopback != nil {
				d.loopback.Close()
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
		// Handshake complete: open this device's loopback /ws and start pumping
		// its replies back to the phone. If the loopback dial fails the device
		// has no destination, so drop it rather than leave a done session with a
		// nil loopback that a later data frame would deref.
		if err := t.openLoopback(ctx, d); err != nil {
			t.removeDevice(f.Device)
			return err
		}
		return nil

	case frameData:
		d := t.device(f.Device)
		if d == nil || !d.sess.done || d.loopback == nil {
			return fmt.Errorf("data with no ready session")
		}
		plaintext, err := d.sess.decrypt(f.Payload)
		if err != nil {
			return err
		}
		// The decrypted payload is a /ws message; hand it to the local server
		// verbatim, exactly as a browser tab would send it.
		return d.loopback.WriteMessage(websocket.TextMessage, plaintext)

	default:
		return nil
	}
}

// openLoopback dials the local /ws for one device and starts its reply pump.
func (t *Tunnel) openLoopback(ctx context.Context, d *device) error {
	loopURL := t.cfg.LoopbackURL
	if t.cfg.AccessKey != "" {
		if u, err := url.Parse(loopURL); err == nil {
			q := u.Query()
			q.Set("access_key", t.cfg.AccessKey)
			u.RawQuery = q.Encode()
			loopURL = u.String()
		}
	}
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, loopURL, nil)
	if err != nil {
		return fmt.Errorf("dial loopback /ws: %w", err)
	}
	t.mu.Lock()
	d.loopback = ws
	t.mu.Unlock()
	t.logf("[tunnel] device=%s bridged to loopback /ws", d.id)
	go t.loopbackPump(d)
	return nil
}

// loopbackPump reads server events from a device's loopback /ws, encrypts them,
// and forwards them to that phone through the relay. It ends when either socket
// closes.
func (t *Tunnel) loopbackPump(d *device) {
	for {
		_, msg, err := d.loopback.ReadMessage()
		if err != nil {
			return
		}
		ciphertext, err := d.sess.encrypt(msg)
		if err != nil {
			t.logf("[tunnel] device=%s encrypt: %v", d.id, err)
			return
		}
		if err := t.writeRelay(frame{Type: frameData, Device: d.id, Payload: ciphertext}); err != nil {
			return
		}
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

func (t *Tunnel) removeDevice(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.devices, id)
}
