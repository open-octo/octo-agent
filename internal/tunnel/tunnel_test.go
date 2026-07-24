package tunnel

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// This test exercises the host tunnel in isolation. The production code under
// test is Tunnel: it demultiplexes a device's shim frames onto the loopback
// server — replaying http-req as HTTP calls and ws-open/msg as loopback /ws
// streams. The relay, the phone, and the local server are stubs speaking the
// same wire protocol. Because the test is in-package, the phone stub reuses the
// real Noise session and both frame codecs.

var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func wsScheme(s *httptest.Server) string { return "ws" + strings.TrimPrefix(s.URL, "http") }

// --- stub loopback server: /api/echo (HTTP) + /ws (WebSocket echo) ---

func startStubLoopback(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	// Echoes the method and request body back as JSON, so the test can prove a
	// real HTTP round-trip happened through the tunnel.
	mux.HandleFunc("/api/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"method":%q,"body":%q}`, r.Method, string(body))
	})
	// Echoes each WebSocket message with a "reply-to:" prefix.
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if err := ws.WriteMessage(websocket.TextMessage, []byte("reply-to:"+string(msg))); err != nil {
				return
			}
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// --- stub relay (broker + bridge), minimal, speaking the relay wire protocol ---

type stubRelay struct {
	srv *httptest.Server

	mu        sync.Mutex
	host      *websocket.Conn
	hostW     sync.Mutex
	hostReady chan struct{}
	hostOnce  sync.Once
	tokens    map[string]bool
	devices   map[string]*websocket.Conn
	deviceW   map[string]*sync.Mutex
	seq       int
}

func startStubRelay(t *testing.T, tokens ...string) *stubRelay {
	t.Helper()
	r := &stubRelay{
		hostReady: make(chan struct{}),
		tokens:    map[string]bool{},
		devices:   map[string]*websocket.Conn{},
		deviceW:   map[string]*sync.Mutex{},
	}
	for _, tok := range tokens {
		r.tokens[tok] = true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/host", r.handleHost)
	mux.HandleFunc("/device", r.handleDevice)
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

func (r *stubRelay) waitHost(t *testing.T) {
	t.Helper()
	select {
	case <-r.hostReady:
	case <-time.After(3 * time.Second):
		t.Fatal("host tunnel never connected to the relay")
	}
}

func (r *stubRelay) writeHost(f frame) error {
	r.hostW.Lock()
	defer r.hostW.Unlock()
	return writeFrame(r.host, f)
}

func (r *stubRelay) writeDevice(id string, f frame) error {
	r.mu.Lock()
	c, m := r.devices[id], r.deviceW[id]
	r.mu.Unlock()
	if c == nil {
		return nil
	}
	m.Lock()
	defer m.Unlock()
	return writeFrame(c, f)
}

func (r *stubRelay) handleHost(w http.ResponseWriter, req *http.Request) {
	ws, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	r.mu.Lock()
	r.host = ws
	r.mu.Unlock()
	r.hostOnce.Do(func() { close(r.hostReady) })
	for {
		f, err := readFrame(ws)
		if err != nil {
			return
		}
		_ = r.writeDevice(f.Device, f)
	}
}

func (r *stubRelay) handleDevice(w http.ResponseWriter, req *http.Request) {
	token := req.URL.Query().Get("token")
	r.mu.Lock()
	ok := r.tokens[token]
	delete(r.tokens, token)
	r.mu.Unlock()
	if !ok {
		http.Error(w, "bad token", http.StatusForbidden)
		return
	}
	ws, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	r.mu.Lock()
	r.seq++
	id := "dev-" + itoa(r.seq)
	r.devices[id] = ws
	r.deviceW[id] = &sync.Mutex{}
	r.mu.Unlock()

	_ = r.writeHost(frame{Type: frameDeviceJoined, Device: id})
	r.deviceW[id].Lock()
	_ = writeFrame(ws, frame{Type: framePaired, Device: id})
	r.deviceW[id].Unlock()

	for {
		f, err := readFrame(ws)
		if err != nil {
			return
		}
		f.Device = id
		_ = r.writeHost(f)
	}
}

// --- mock phone (initiator), reusing the real session + both frame codecs ---

type mockPhone struct {
	ws       *websocket.Conn
	sess     *session
	deviceID string
	ready    chan struct{}
	recvCh   chan shimFrame
}

func (r *stubRelay) dialPhone(t *testing.T, token string) *mockPhone {
	t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial(wsScheme(r.srv)+"/device?token="+token, nil)
	if err != nil {
		t.Fatalf("dial phone: %v", err)
	}
	p := &mockPhone{ws: ws, ready: make(chan struct{}), recvCh: make(chan shimFrame, 16)}
	go p.loop()
	return p
}

func (p *mockPhone) loop() {
	for {
		f, err := readFrame(p.ws)
		if err != nil {
			return
		}
		switch f.Type {
		case framePaired:
			p.deviceID = f.Device
			kp, err := generateKeypair()
			if err != nil {
				return
			}
			s, err := newSession(true /* initiator */, kp)
			if err != nil {
				return
			}
			p.sess = s
			msg, err := s.write(nil)
			if err != nil {
				return
			}
			_ = writeFrame(p.ws, frame{Type: frameHandshake, Payload: msg})
		case frameHandshake:
			if _, err := p.sess.read(f.Payload); err != nil {
				return
			}
			if !p.sess.done {
				msg, err := p.sess.write(nil)
				if err != nil {
					return
				}
				_ = writeFrame(p.ws, frame{Type: frameHandshake, Payload: msg})
			}
			if p.sess.done {
				close(p.ready)
			}
		case frameData:
			pt, err := p.sess.decrypt(f.Payload)
			if err != nil {
				return
			}
			sf, err := decodeShimFrame(pt)
			if err != nil {
				return
			}
			p.recvCh <- sf
		}
	}
}

func (p *mockPhone) waitReady(t *testing.T) {
	t.Helper()
	select {
	case <-p.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("phone handshake never completed")
	}
}

func (p *mockPhone) sendShim(t *testing.T, f shimFrame) {
	t.Helper()
	data, err := f.encode()
	if err != nil {
		t.Fatalf("phone encode: %v", err)
	}
	ct, err := p.sess.encrypt(data)
	if err != nil {
		t.Fatalf("phone encrypt: %v", err)
	}
	if err := writeFrame(p.ws, frame{Type: frameData, Payload: ct}); err != nil {
		t.Fatalf("phone send: %v", err)
	}
}

func (p *mockPhone) recvShim(t *testing.T) shimFrame {
	t.Helper()
	select {
	case sf := <-p.recvCh:
		return sf
	case <-time.After(3 * time.Second):
		t.Fatal("phone received no frame")
	}
	return shimFrame{}
}

func newPairedPhone(t *testing.T) *mockPhone {
	t.Helper()
	stub := startStubLoopback(t)
	relay := startStubRelay(t, "tok-1")
	tun, err := New(Config{
		RelayURL:    wsScheme(relay.srv),
		TunnelID:    "tunnel-A",
		PairTokens:  []string{"tok-1"},
		LoopbackURL: wsScheme(stub) + "/ws",
		Logf:        func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("new tunnel: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = tun.Serve(ctx) }()

	relay.waitHost(t)
	phone := relay.dialPhone(t, "tok-1")
	phone.waitReady(t)
	return phone
}

// TestHostTunnel_HTTPRoundTrip proves an /api call tunnels: the phone's http-req
// frame is replayed as a real loopback HTTP request and its response comes back
// as an http-resp frame.
func TestHostTunnel_HTTPRoundTrip(t *testing.T) {
	phone := newPairedPhone(t)

	phone.sendShim(t, shimFrame{
		Kind:    shimHTTPReq,
		ID:      "r1",
		Method:  "POST",
		Path:    "/api/echo",
		Headers: map[string]string{"content-type": "application/json"},
		Body:    strPtr(`{"hi":1}`),
	})

	resp := phone.recvShim(t)
	if resp.Kind != shimHTTPResp || resp.ID != "r1" {
		t.Fatalf("got %+v, want an http-resp for r1", resp)
	}
	if resp.Status != http.StatusCreated {
		t.Errorf("status = %d, want %d", resp.Status, http.StatusCreated)
	}
	if resp.Body == nil || !strings.Contains(*resp.Body, `"method":"POST"`) || !strings.Contains(*resp.Body, `{\"hi\":1}`) {
		t.Errorf("body = %v, want it to echo the POST + request body", resp.Body)
	}
}

// TestHostTunnel_WSRoundTrip proves a /ws stream tunnels: ws-open dials the
// loopback /ws, ws-msg is delivered, and the server's reply comes back as a
// ws-msg frame tagged with the same stream id.
func TestHostTunnel_WSRoundTrip(t *testing.T) {
	phone := newPairedPhone(t)

	phone.sendShim(t, shimFrame{Kind: shimWSOpen, ID: "w1", Path: "/ws"})
	phone.sendShim(t, shimFrame{Kind: shimWSMessage, ID: "w1", Data: "hello"})

	msg := phone.recvShim(t)
	if msg.Kind != shimWSMessage || msg.ID != "w1" {
		t.Fatalf("got %+v, want a ws-msg for w1", msg)
	}
	if msg.Data != "reply-to:hello" {
		t.Errorf("data = %q, want %q", msg.Data, "reply-to:hello")
	}
}

// TestHostTunnel_WSErrorOnBadPath proves a ws-open to a path the loopback server
// won't upgrade comes back as a ws-error, not a hang.
func TestHostTunnel_WSErrorOnBadPath(t *testing.T) {
	phone := newPairedPhone(t)

	phone.sendShim(t, shimFrame{Kind: shimWSOpen, ID: "w9", Path: "/api/echo"})

	f := phone.recvShim(t)
	if f.Kind != shimWSError || f.ID != "w9" {
		t.Fatalf("got %+v, want a ws-error for w9", f)
	}
}

// itoa avoids importing strconv for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// TestRelayDialBase: the SNI-subdomain rewrite applies to DNS-named relays
// only — IP literals and dotless hosts (local dev) dial unchanged, and the
// port survives the rewrite.
func TestRelayDialBase(t *testing.T) {
	cases := []struct{ relay, tunnel, want string }{
		{"wss://relay.octo.dev", "t123", "wss://t123.relay.octo.dev"},
		{"wss://relay.octo.dev:8443", "t123", "wss://t123.relay.octo.dev:8443"},
		{"ws://127.0.0.1:8090", "t123", "ws://127.0.0.1:8090"},
		{"ws://localhost:8090", "t123", "ws://localhost:8090"},
		{"ws://[::1]:8090", "t123", "ws://[::1]:8090"},
		{"wss://relay.octo.dev", "", "wss://relay.octo.dev"},
	}
	for _, c := range cases {
		if got := relayDialBase(c.relay, c.tunnel); got != c.want {
			t.Errorf("relayDialBase(%q, %q) = %q, want %q", c.relay, c.tunnel, got, c.want)
		}
	}
}

// TestRelayDialBase_RuleParity: shapes where the eligibility rule must match
// the mobile natives exactly — trailing-dot IPs, dots-and-digits non-IPs, and
// non-DNS-label tunnel ids all fall back to the unchanged (query-routed) URL.
func TestRelayDialBase_RuleParity(t *testing.T) {
	cases := []struct{ relay, tunnel, want string }{
		{"ws://127.0.0.1.:8090", "t123", "ws://127.0.0.1.:8090"},    // trailing-dot IP
		{"ws://1.2.3.4.5:8090", "t123", "ws://1.2.3.4.5:8090"},      // digits+dots, not an IP
		{"wss://relay.octo.dev", "has.dot", "wss://relay.octo.dev"}, // id not a DNS label
		{"wss://relay.octo.dev", "UPPER", "wss://relay.octo.dev"},   // id not lowercase
	}
	for _, c := range cases {
		if got := relayDialBase(c.relay, c.tunnel); got != c.want {
			t.Errorf("relayDialBase(%q, %q) = %q, want %q", c.relay, c.tunnel, got, c.want)
		}
	}
}
