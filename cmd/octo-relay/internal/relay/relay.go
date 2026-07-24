// Package relay implements the single-node PoC of octo-relay: a dumb pipe that
// bridges a host to its paired devices and brokers pairing. It routes opaque
// frames by (tunnel, device) and never inspects a payload — the two ends run a
// Noise session the relay has no key for, so everything it copies is ciphertext.
//
// Beyond bridging, the relay consumes two kinds of host control frames:
// pairing-token offers (in the /host query) and wakeup requests (M1c), which
// it turns into content-free APNs/FCM pushes without ever persisting or
// logging a token. Multi-node routing rides the TLS SNI subdomain and lives
// entirely in the balancer (M1b) — nodes share nothing.
package relay

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/open-octo/octo-agent/cmd/octo-relay/internal/wire"
)

// conn wraps a WebSocket with a write mutex. gorilla/websocket forbids
// concurrent writers, and a host socket is written by every device read loop
// (N phones → 1 host) while a device socket is written by the host read loop.
type conn struct {
	ws     *websocket.Conn
	writeM sync.Mutex
}

func (c *conn) write(f wire.Frame) error {
	c.writeM.Lock()
	defer c.writeM.Unlock()
	return f.WriteTo(c.ws)
}

// tunnel is a host's presence on the relay plus the devices bridged to it.
type tunnel struct {
	host    *conn
	devices map[string]*conn // deviceID -> device connection
}

// WakePusher fires one content-free push (see internal/push). nil disables
// wakeups — frames are dropped with a token-free log line.
type WakePusher interface {
	Wake(ctx context.Context, platform, token string) error
}

// Relay is the in-memory bridge/broker. All maps are guarded by mu. Nothing
// here is persisted: pairing tokens, device ids, and connections all live only
// as long as the process and the sockets do.
type Relay struct {
	// Pusher handles wakeup frames. Set at startup; never mutated after.
	Pusher WakePusher

	mu      sync.Mutex
	tunnels map[string]*tunnel // tunnelID -> tunnel
	tokens  map[string]string  // one-time pairing token -> tunnelID
	seq     int                // device-id counter

	// observe/observed instrument the PoC test: when observe is set, every
	// forwarded payload is captured so a test can assert the relay only ever
	// saw ciphertext. Off in production.
	observe  bool
	observed [][]byte

	Quiet bool // suppress connection logging (tests set this)
}

// New returns a production-shaped relay (no payload capture).
func New() *Relay {
	return &Relay{tunnels: map[string]*tunnel{}, tokens: map[string]string{}}
}

// NewInstrumented returns a relay that records every forwarded payload, for the
// end-to-end ciphertext assertion in tests.
func NewInstrumented() *Relay {
	r := New()
	r.observe = true
	r.Quiet = true
	return r
}

// Observed returns a copy of every payload the relay has forwarded so far.
func (r *Relay) Observed() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]byte, len(r.observed))
	copy(out, r.observed)
	return out
}

var upgrader = websocket.Upgrader{
	// The relay's clients are the host goroutine and the phone shim, never a
	// browser, so there is no Origin to police here.
	CheckOrigin: func(*http.Request) bool { return true },
}

// Handler routes the three relay endpoints. Production would front this with an
// SNI-hashing load balancer; the PoC serves them directly.
func (r *Relay) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/host", r.handleHost)
	mux.HandleFunc("/device", r.handleDevice)
	return mux
}

func (r *Relay) logf(format string, args ...any) {
	if !r.Quiet {
		log.Printf(format, args...)
	}
}

// tunnelIDFromRequest resolves the tunnel a request addresses. The query
// parameter wins (every current client sends it); when it's absent the first
// DNS label of the Host is used — production clients dial
// <tunnelid>.relay.octo.dev so the L4 balancer can consistent-hash the SNI,
// and a future query-less client still routes. A subdomain-less direct dial
// with no query yields a junk-but-harmless id (e.g. "relay"); the query check
// runs first precisely so that never happens with today's clients.
func tunnelIDFromRequest(req *http.Request) string {
	if id := req.URL.Query().Get("tunnel"); id != "" {
		return id
	}
	host := req.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if net.ParseIP(host) != nil {
		return ""
	}
	label, _, found := strings.Cut(host, ".")
	if !found {
		return ""
	}
	return label
}

// handleHost registers the host side of a tunnel. The host offers a one-time
// pairing token (in production, the one encoded in its QR) that a phone will
// present to find it.
func (r *Relay) handleHost(w http.ResponseWriter, req *http.Request) {
	tunnelID := tunnelIDFromRequest(req)
	// A host declares the one-time pairing tokens it will honor (in production,
	// one per QR it mints). Repeated ?pairtoken= values let several phones pair.
	tokens := req.URL.Query()["pairtoken"]
	if tunnelID == "" {
		http.Error(w, "missing tunnel", http.StatusBadRequest)
		return
	}
	ws, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	c := &conn{ws: ws}

	r.mu.Lock()
	r.tunnels[tunnelID] = &tunnel{host: c, devices: map[string]*conn{}}
	for _, tok := range tokens {
		if tok != "" {
			r.tokens[tok] = tunnelID
		}
	}
	r.mu.Unlock()
	r.logf("[relay] host connected tunnel=%s", tunnelID)

	defer func() {
		r.mu.Lock()
		delete(r.tunnels, tunnelID)
		for _, tok := range tokens {
			delete(r.tokens, tok)
		}
		r.mu.Unlock()
		ws.Close()
		r.logf("[relay] host disconnected tunnel=%s", tunnelID)
	}()

	// Host → device: the host names the recipient in frame.Device. Wakeup
	// frames are the exception — the relay consumes those itself.
	//
	// wakeBudget rate-limits wakeups per host connection: /host is
	// unauthenticated, and the host-side 30s/phone limit is voluntary — an
	// abusive client must not be able to burn the operator's APNs/FCM
	// credentials or spawn unbounded push goroutines. Per-connection local
	// state, refilled once a minute; over-budget frames are dropped with a
	// token-free log line.
	wakeBudget := wakeupsPerMinute
	wakeWindow := time.Now()
	for {
		f, err := wire.Read(ws)
		if err != nil {
			return
		}
		if f.Type == wire.TypeWakeup {
			if now := time.Now(); now.Sub(wakeWindow) >= time.Minute {
				wakeBudget = wakeupsPerMinute
				wakeWindow = now
			}
			if wakeBudget <= 0 {
				r.logf("[relay] wakeup rate limit exceeded tunnel=%s", tunnelID)
				continue
			}
			wakeBudget--
			// Not captured: a wakeup payload is operational metadata (push
			// token), not forwarded ciphertext — the instrumented test's
			// "everything forwarded is ciphertext" assertion must not see it.
			r.handleWakeup(req.Context(), tunnelID, f)
			continue
		}
		r.capture(f.Payload)
		r.mu.Lock()
		t := r.tunnels[tunnelID]
		var dev *conn
		if t != nil {
			dev = t.devices[f.Device]
		}
		r.mu.Unlock()
		if dev == nil {
			r.logf("[relay] drop host frame for unknown device=%s", f.Device)
			continue
		}
		if err := dev.write(f); err != nil {
			r.logf("[relay] write to device=%s: %v", f.Device, err)
		}
	}
}

// handleDevice pairs a new phone: it presents a one-time token, the relay
// matches it to a waiting host, assigns a device id, and thereafter bridges
// this socket to that host. The token is spent on match.
func (r *Relay) handleDevice(w http.ResponseWriter, req *http.Request) {
	token := req.URL.Query().Get("token")

	r.mu.Lock()
	tunnelID, ok := r.tokens[token]
	t := r.tunnels[tunnelID]
	if !ok || t == nil {
		r.mu.Unlock()
		http.Error(w, "unknown or spent pairing token", http.StatusForbidden)
		return
	}
	delete(r.tokens, token) // single-use
	r.mu.Unlock()

	ws, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	c := &conn{ws: ws}

	r.mu.Lock()
	r.seq++
	deviceID := fmt.Sprintf("dev-%d", r.seq)
	t = r.tunnels[tunnelID] // re-read: host may have dropped during upgrade
	if t == nil {
		r.mu.Unlock()
		ws.Close()
		return
	}
	t.devices[deviceID] = c
	host := t.host
	r.mu.Unlock()
	r.logf("[relay] device paired tunnel=%s device=%s", tunnelID, deviceID)

	// Tell the phone its assigned id, and tell the host a device has joined so
	// it can spin up the responder side of the Noise handshake for it.
	_ = c.write(wire.Frame{Type: wire.TypePaired, Tunnel: tunnelID, Device: deviceID})
	_ = host.write(wire.Frame{Type: wire.TypeDeviceJoined, Tunnel: tunnelID, Device: deviceID})

	defer func() {
		r.mu.Lock()
		if t := r.tunnels[tunnelID]; t != nil {
			delete(t.devices, deviceID)
		}
		r.mu.Unlock()
		ws.Close()
		r.logf("[relay] device disconnected tunnel=%s device=%s", tunnelID, deviceID)
	}()

	// Device → host: the relay stamps the addressing so the host knows which
	// paired phone a frame came from. The device never has to name its tunnel.
	for {
		f, err := wire.Read(ws)
		if err != nil {
			return
		}
		r.capture(f.Payload)
		f.Tunnel = tunnelID
		f.Device = deviceID
		r.mu.Lock()
		t := r.tunnels[tunnelID]
		var h *conn
		if t != nil {
			h = t.host
		}
		r.mu.Unlock()
		if h == nil {
			return
		}
		if err := h.write(f); err != nil {
			r.logf("[relay] write to host tunnel=%s: %v", tunnelID, err)
		}
	}
}

// wakeupsPerMinute caps wakeup frames per host connection. Generous for real
// hosts (which self-limit to one per phone per 30s) while bounding abuse.
const wakeupsPerMinute = 10

// handleWakeup consumes a host's wakeup frame: skip if the target phone is
// currently connected (it gets the update over the live tunnel), otherwise
// fire a content-free push. The token exists only in the frame and the push
// request — never in a log line, never on disk.
func (r *Relay) handleWakeup(ctx context.Context, tunnelID string, f wire.Frame) {
	r.mu.Lock()
	t := r.tunnels[tunnelID]
	connected := t != nil && f.Device != "" && t.devices[f.Device] != nil
	r.mu.Unlock()
	if connected {
		return
	}
	p, err := wire.DecodeWakeup(f.Payload)
	if err != nil || p.PushToken == "" {
		r.logf("[relay] wakeup with bad payload tunnel=%s device=%s", tunnelID, f.Device)
		return
	}
	if r.Pusher == nil {
		r.logf("[relay] wakeup dropped (no pusher configured) tunnel=%s device=%s", tunnelID, f.Device)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if err := r.Pusher.Wake(ctx, p.Platform, p.PushToken); err != nil {
			r.logf("[relay] wakeup push failed tunnel=%s device=%s platform=%s: %v", tunnelID, f.Device, p.Platform, err)
			return
		}
		r.logf("[relay] wakeup pushed tunnel=%s device=%s platform=%s", tunnelID, f.Device, p.Platform)
	}()
}

// capture records a forwarded payload when instrumented. It is the hook the
// end-to-end test uses to prove the relay only ever handled ciphertext.
func (r *Relay) capture(payload []byte) {
	if !r.observe || len(payload) == 0 {
		return
	}
	cp := make([]byte, len(payload))
	copy(cp, payload)
	r.mu.Lock()
	r.observed = append(r.observed, cp)
	r.mu.Unlock()
}
