package tunnel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// This test exercises the host tunnel in isolation. The production code under
// test is Tunnel (relay ↔ loopback /ws bridge). The relay, the phone, and the
// local /ws are stubs speaking the same wire protocol — the tunnel cannot tell
// them from the real thing. Because the test is in-package, the phone stub
// reuses the real Noise session and frame codec instead of duplicating them.

var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func wsScheme(s *httptest.Server) string { return "ws" + strings.TrimPrefix(s.URL, "http") }

// --- stub local /ws (the loopback target the tunnel bridges into) ---

type stubWS struct {
	srv      *httptest.Server
	received chan string // plaintext the tunnel forwarded in from a phone
	keys     chan string // access_key the tunnel presented on connect
}

func startStubWS(t *testing.T) *stubWS {
	t.Helper()
	s := &stubWS{received: make(chan string, 8), keys: make(chan string, 8)}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.keys <- r.URL.Query().Get("access_key")
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
			s.received <- string(msg)
			// Exercise the loopback→phone direction: reply to what we got.
			if err := ws.WriteMessage(websocket.TextMessage, []byte("reply-to:"+string(msg))); err != nil {
				return
			}
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// --- stub relay (broker + bridge), minimal, speaking the wire protocol ---

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

// waitHost blocks until the host tunnel has connected — mirrors reality, where a
// long-lived host is present before any phone pairs.
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
	// Host → device: route by the device the host named.
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

	// Announce to both ends, host first so it is ready before the phone's first
	// handshake frame arrives.
	_ = r.writeHost(frame{Type: frameDeviceJoined, Device: id})
	r.deviceW[id].Lock()
	_ = writeFrame(ws, frame{Type: framePaired, Device: id})
	r.deviceW[id].Unlock()

	// Device → host: stamp the device id so the host knows the source.
	for {
		f, err := readFrame(ws)
		if err != nil {
			return
		}
		f.Device = id
		_ = r.writeHost(f)
	}
}

// --- mock phone (initiator), reusing the real session + wire ---

type mockPhone struct {
	ws       *websocket.Conn
	sess     *session
	deviceID string
	ready    chan struct{}
	recvCh   chan string
}

func (r *stubRelay) dialPhone(t *testing.T, token string) *mockPhone {
	t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial(wsScheme(r.srv)+"/device?token="+token, nil)
	if err != nil {
		t.Fatalf("dial phone: %v", err)
	}
	p := &mockPhone{ws: ws, ready: make(chan struct{}), recvCh: make(chan string, 8)}
	go p.loop(t)
	return p
}

func (p *mockPhone) loop(t *testing.T) {
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
			p.recvCh <- string(pt)
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

func (p *mockPhone) send(t *testing.T, plaintext string) {
	t.Helper()
	ct, err := p.sess.encrypt([]byte(plaintext))
	if err != nil {
		t.Fatalf("phone encrypt: %v", err)
	}
	if err := writeFrame(p.ws, frame{Type: frameData, Payload: ct}); err != nil {
		t.Fatalf("phone send: %v", err)
	}
}

func (p *mockPhone) recv(t *testing.T) string {
	t.Helper()
	select {
	case m := <-p.recvCh:
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("phone received nothing")
	}
	return ""
}

func recvWithin(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting on channel")
	}
	return ""
}

// TestHostTunnel_BridgesBothDirections is the isolated proof: a phone pairs and
// runs Noise with the host tunnel through a stub relay; a message the phone
// encrypts arrives as plaintext at the local /ws, and the /ws's reply arrives
// back at the phone decrypted. The tunnel decrypts and re-encrypts host-side —
// the local server sees an ordinary /ws client.
func TestHostTunnel_BridgesBothDirections(t *testing.T) {
	stub := startStubWS(t)
	relay := startStubRelay(t, "tok-1")

	tun, err := New(Config{
		RelayURL:    wsScheme(relay.srv),
		TunnelID:    "tunnel-A",
		PairTokens:  []string{"tok-1"},
		LoopbackURL: wsScheme(stub.srv),
		AccessKey:   "secret-key",
		Logf:        func(string, ...any) {}, // quiet
	})
	if err != nil {
		t.Fatalf("new tunnel: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = tun.Serve(ctx) }()

	relay.waitHost(t) // a phone only pairs once the long-lived host is present
	phone := relay.dialPhone(t, "tok-1")
	phone.waitReady(t)

	// Phone → local /ws: the plaintext must surface at the loopback verbatim.
	phone.send(t, "REQ-hello-🔒")
	if got := recvWithin(t, stub.received); got != "REQ-hello-🔒" {
		t.Errorf("loopback received %q, want %q", got, "REQ-hello-🔒")
	}

	// The tunnel presented the access key to the local /ws.
	if got := recvWithin(t, stub.keys); got != "secret-key" {
		t.Errorf("tunnel presented access_key %q, want %q", got, "secret-key")
	}

	// Local /ws → phone: the stub's reply must arrive decrypted at the phone.
	if got := phone.recv(t); got != "reply-to:REQ-hello-🔒" {
		t.Errorf("phone received %q, want %q", got, "reply-to:REQ-hello-🔒")
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
