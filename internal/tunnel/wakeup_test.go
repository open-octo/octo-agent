package tunnel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// wakeRelay is a minimal /host endpoint that records every frame the tunnel
// writes, so the test can assert exactly which wakeups went out.
type wakeRelay struct {
	srv       *httptest.Server
	frames    chan frame
	connected chan struct{}
	once      sync.Once
}

func startWakeRelay(t *testing.T) *wakeRelay {
	t.Helper()
	r := &wakeRelay{frames: make(chan frame, 16), connected: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/host", func(w http.ResponseWriter, req *http.Request) {
		ws, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			return
		}
		r.once.Do(func() { close(r.connected) })
		for {
			f, err := readFrame(ws)
			if err != nil {
				return
			}
			r.frames <- f
		}
	})
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

func (r *wakeRelay) waitConnected(t *testing.T) {
	t.Helper()
	select {
	case <-r.connected:
	case <-time.After(3 * time.Second):
		t.Fatal("tunnel never connected to the wake relay")
	}
}

// activityLoopback is a loopback /ws stub the watcher connects to; the test
// broadcasts session_activity events through it.
type activityLoopback struct {
	srv   *httptest.Server
	mu    sync.Mutex
	conns []*websocket.Conn
	ready chan struct{}
	once  sync.Once
}

func startActivityLoopback(t *testing.T) *activityLoopback {
	t.Helper()
	l := &activityLoopback{ready: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, req *http.Request) {
		ws, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			return
		}
		l.mu.Lock()
		l.conns = append(l.conns, ws)
		l.mu.Unlock()
		l.once.Do(func() { close(l.ready) })
		for { // hold the connection; the watcher only reads
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	})
	l.srv = httptest.NewServer(mux)
	t.Cleanup(l.srv.Close)
	return l
}

func (l *activityLoopback) broadcast(t *testing.T, typ, kind string) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"type": typ, "kind": kind})
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, ws := range l.conns {
		if err := ws.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("broadcast: %v", err)
		}
	}
}

func (l *activityLoopback) waitWatcher(t *testing.T) {
	t.Helper()
	select {
	case <-l.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("activity watcher never connected to loopback /ws")
	}
}

func expectWakeup(t *testing.T, r *wakeRelay, wantDevice, wantToken, wantPlatform string) {
	t.Helper()
	select {
	case f := <-r.frames:
		if f.Type != frameWakeup {
			t.Fatalf("frame type = %q, want wakeup", f.Type)
		}
		if f.Device != wantDevice {
			t.Errorf("device = %q, want %q", f.Device, wantDevice)
		}
		var p wakeupPayload
		if err := json.Unmarshal(f.Payload, &p); err != nil {
			t.Fatalf("payload: %v", err)
		}
		if p.PushToken != wantToken || p.Platform != wantPlatform {
			t.Errorf("payload = %+v, want token %q platform %q", p, wantToken, wantPlatform)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no wakeup frame arrived")
	}
}

// waitRelayReady blocks until the tunnel has assigned its relay connection,
// so a wakeup write can actually leave (white-box: same package).
func waitRelayReady(t *testing.T, tun *Tunnel) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		tun.relayMu.Lock()
		ready := tun.relay != nil
		tun.relayMu.Unlock()
		if ready {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("tunnel never assigned its relay connection")
}

func expectNoFrame(t *testing.T, r *wakeRelay, within time.Duration) {
	t.Helper()
	select {
	case f := <-r.frames:
		t.Fatalf("unexpected frame %+v", f)
	case <-time.After(within):
	}
}

// TestWakeupFlow: a registered token produces exactly one rate-limited wakeup
// frame per qualifying activity; non-qualifying kinds and unregistration
// produce none.
func TestWakeupFlow(t *testing.T) {
	relay := startWakeRelay(t)
	loop := startActivityLoopback(t)

	tun, err := New(Config{
		RelayURL:    wsScheme(relay.srv),
		TunnelID:    "t1",
		LoopbackURL: wsScheme(loop.srv) + "/ws",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = tun.Serve(ctx) }()
	loop.waitWatcher(t)
	relay.waitConnected(t)
	// waitConnected fires when the SERVER upgrades /host, but the tunnel
	// goroutine assigns t.relay a beat later. If the first broadcast raced
	// ahead of that, wakeDevices' writeRelay would fail and silently drop the
	// (un-retried) wakeup. Poll the client side until it's ready to send.
	waitRelayReady(t, tun)

	tun.registerPushToken([]byte("peer-static-1"), "dev-1", "tokenA", "apns")

	loop.broadcast(t, "session_activity", "question_pending")
	expectWakeup(t, relay, "dev-1", "tokenA", "apns")

	// Immediately again: inside the rate-limit window, nothing goes out.
	loop.broadcast(t, "session_activity", "turn_complete")
	expectNoFrame(t, relay, 400*time.Millisecond)

	// Non-qualifying kinds never wake, and a re-register must not reset the
	// rate-limit window.
	tun.registerPushToken([]byte("peer-static-1"), "dev-1", "tokenA", "apns")
	loop.broadcast(t, "session_activity", "question_resolved")
	loop.broadcast(t, "other_event", "question_pending")
	expectNoFrame(t, relay, 400*time.Millisecond)

	// Unregister (empty token): later activity is silent.
	tun.registerPushToken([]byte("peer-static-1"), "dev-1", "", "apns")
	tun.pushMu.Lock()
	if len(tun.pushRegs) != 0 {
		t.Errorf("pushRegs = %d entries after unregister, want 0", len(tun.pushRegs))
	}
	tun.pushMu.Unlock()
	loop.broadcast(t, "session_activity", "confirm_pending")
	expectNoFrame(t, relay, 400*time.Millisecond)
}

// TestBridgeConsumesPushToken: a push-token shim frame lands in the registry
// (keyed by the session's peer static) and is not treated as loopback traffic.
func TestBridgeConsumesPushToken(t *testing.T) {
	tun, err := New(Config{RelayURL: "ws://127.0.0.1:1", TunnelID: "t1", LoopbackURL: "ws://127.0.0.1:1/ws"})
	if err != nil {
		t.Fatal(err)
	}
	sess := &session{peerStatic: []byte("peer-xyz")}
	b := newBridge(context.Background(), tun, "dev-7", sess)

	data, _ := json.Marshal(pushTokenData{Token: "tok123", Platform: "fcm"})
	b.handle(shimFrame{Kind: shimPushToken, Data: string(data)})

	tun.pushMu.Lock()
	defer tun.pushMu.Unlock()
	reg := tun.pushRegs["cGVlci14eXo="] // base64("peer-xyz")
	if reg == nil || reg.token != "tok123" || reg.platform != "fcm" || reg.deviceID != "dev-7" {
		t.Fatalf("registry = %+v, want tok123/fcm/dev-7", reg)
	}
}
