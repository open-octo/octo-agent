package relay_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/cmd/octo-relay/internal/client"
	"github.com/open-octo/octo-agent/cmd/octo-relay/internal/relay"
	"github.com/open-octo/octo-agent/cmd/octo-relay/internal/wire"
)

// wsURL turns an httptest http:// base into a ws:// base.
func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func recvWithin(t *testing.T, c *client.Client, d time.Duration) client.Message {
	t.Helper()
	select {
	case m, ok := <-c.Recv():
		if !ok {
			t.Fatal("recv channel closed before a message arrived")
		}
		return m
	case err := <-c.Err():
		t.Fatalf("client error while waiting for a message: %v", err)
	case <-time.After(d):
		t.Fatal("timed out waiting for a message")
	}
	return client.Message{}
}

// awaitReady blocks until a client's session for a given expected count is up,
// failing fast on a client error.
func awaitReady(t *testing.T, c *client.Client) string {
	t.Helper()
	done := make(chan string, 1)
	go func() { done <- c.WaitReady() }()
	select {
	case dev := <-done:
		return dev
	case err := <-c.Err():
		t.Fatalf("client error during handshake: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the Noise handshake to complete")
	}
	return ""
}

// TestEndToEnd_CiphertextOnly is the core PoC: a phone pairs with a host through
// the relay, both complete a Noise handshake, an application message travels each
// way — and the relay, though it copied every byte, never saw the plaintext.
func TestEndToEnd_CiphertextOnly(t *testing.T) {
	r := relay.NewInstrumented()
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	host, err := client.DialHost(wsURL(srv), "tunnel-A", "tok-1")
	if err != nil {
		t.Fatalf("dial host: %v", err)
	}
	defer host.Close()

	phone, err := client.DialPhone(wsURL(srv), "tok-1")
	if err != nil {
		t.Fatalf("dial phone: %v", err)
	}
	defer phone.Close()

	deviceID := phone.WaitPaired()
	if deviceID == "" {
		t.Fatal("phone was not assigned a device id")
	}

	// Both sides must finish the handshake before either sends application data.
	hostDev := awaitReady(t, host)
	awaitReady(t, phone)
	if hostDev != deviceID {
		t.Fatalf("host paired device %q, phone believes it is %q", hostDev, deviceID)
	}

	const fromPhone = "PING from phone 🔒 phone-secret-plaintext"
	const fromHost = "PONG from host 🔒 host-secret-plaintext"

	// Phone → host.
	if err := phone.Send("", []byte(fromPhone)); err != nil {
		t.Fatalf("phone send: %v", err)
	}
	got := recvWithin(t, host, 3*time.Second)
	if string(got.Data) != fromPhone {
		t.Errorf("host received %q, want %q", got.Data, fromPhone)
	}
	if got.Device != deviceID {
		t.Errorf("host saw device %q, want %q", got.Device, deviceID)
	}

	// Host → phone.
	if err := host.Send(deviceID, []byte(fromHost)); err != nil {
		t.Fatalf("host send: %v", err)
	}
	got = recvWithin(t, phone, 3*time.Second)
	if string(got.Data) != fromHost {
		t.Errorf("phone received %q, want %q", got.Data, fromHost)
	}

	// The guarantee: nothing the relay forwarded contained either plaintext.
	observed := r.Observed()
	if len(observed) == 0 {
		t.Fatal("relay observed no frames; instrumentation broken")
	}
	for i, payload := range observed {
		if bytes.Contains(payload, []byte(fromPhone)) || bytes.Contains(payload, []byte(fromHost)) {
			t.Errorf("relay payload #%d leaked plaintext: %q", i, payload)
		}
	}
	t.Logf("relay forwarded %d frames, all ciphertext", len(observed))
}

// TestTwoDevices_RoutingIsolation proves frames are addressed by (tunnel,
// device): two phones on one host each get only their own reply.
func TestTwoDevices_RoutingIsolation(t *testing.T) {
	r := relay.NewInstrumented()
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	host, err := client.DialHost(wsURL(srv), "tunnel-B", "tok-a", "tok-b")
	if err != nil {
		t.Fatalf("dial host: %v", err)
	}
	defer host.Close()

	phoneA, err := client.DialPhone(wsURL(srv), "tok-a")
	if err != nil {
		t.Fatalf("dial phoneA: %v", err)
	}
	defer phoneA.Close()
	devA := phoneA.WaitPaired()
	awaitReady(t, host)
	awaitReady(t, phoneA)

	phoneB, err := client.DialPhone(wsURL(srv), "tok-b")
	if err != nil {
		t.Fatalf("dial phoneB: %v", err)
	}
	defer phoneB.Close()
	devB := phoneB.WaitPaired()
	awaitReady(t, host)
	awaitReady(t, phoneB)

	if devA == devB {
		t.Fatalf("both phones got the same device id %q", devA)
	}

	// Host sends a distinct message to each device.
	msgA := []byte("for-A-only")
	msgB := []byte("for-B-only")
	if err := host.Send(devA, msgA); err != nil {
		t.Fatalf("send to A: %v", err)
	}
	if err := host.Send(devB, msgB); err != nil {
		t.Fatalf("send to B: %v", err)
	}

	gotA := recvWithin(t, phoneA, 3*time.Second)
	gotB := recvWithin(t, phoneB, 3*time.Second)
	if string(gotA.Data) != string(msgA) {
		t.Errorf("phoneA got %q, want %q", gotA.Data, msgA)
	}
	if string(gotB.Data) != string(msgB) {
		t.Errorf("phoneB got %q, want %q", gotB.Data, msgB)
	}
}

// TestUnknownToken rejects a phone whose pairing token was never offered.
func TestUnknownToken(t *testing.T) {
	r := relay.New()
	r.Quiet = true
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	host, err := client.DialHost(wsURL(srv), "tunnel-C", "good-token")
	if err != nil {
		t.Fatalf("dial host: %v", err)
	}
	defer host.Close()

	if _, err := client.DialPhone(wsURL(srv), "bad-token"); err == nil {
		t.Fatal("phone with an unknown token should be rejected")
	}
}

// TestTunnelIDFromRequest: query wins; a subdomain Host fills in when the
// query is absent (SNI-routed production clients); IP/dotless hosts without
// a query resolve to nothing.
func TestTunnelIDFromRequest(t *testing.T) {
	cases := []struct{ url, host, want string }{
		{"/host?tunnel=t123", "relay.octo.dev", "t123"},
		{"/host?tunnel=t123", "t999.relay.octo.dev", "t123"}, // query beats subdomain
		{"/host", "t123.relay.octo.dev", "t123"},
		{"/host", "t123.relay.octo.dev:443", "t123"},
		{"/host", "127.0.0.1:8090", ""},
		{"/host", "localhost:8090", ""},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", c.url, nil)
		req.Host = c.host
		if got := relay.TunnelIDFromRequest(req); got != c.want {
			t.Errorf("TunnelIDFromRequest(url=%q host=%q) = %q, want %q", c.url, c.host, got, c.want)
		}
	}
}

// fakePusher records wake calls for assertions.
type fakePusher struct {
	calls chan [2]string // {platform, token}
}

func (f *fakePusher) Wake(_ context.Context, platform, token string) error {
	f.calls <- [2]string{platform, token}
	return nil
}

// TestWakeupConsumption: a wakeup for an offline device fires the pusher with
// exactly the token/platform from the payload; a wakeup for a connected
// device is dropped (the phone gets the update over the live tunnel); the
// frame is never forwarded to any device.
func TestWakeupConsumption(t *testing.T) {
	fake := &fakePusher{calls: make(chan [2]string, 4)}
	r := relay.New()
	r.Quiet = true
	r.Pusher = fake
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	host, err := client.DialHost(wsURL(srv), "tunnel-W", "tok-w1")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()

	wakeup := func(device, token, platform string) {
		t.Helper()
		payload, _ := json.Marshal(wire.WakeupPayload{PushToken: token, Platform: platform})
		if err := host.WriteRawFrame(wire.Frame{Type: wire.TypeWakeup, Device: device, Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}

	// Offline device: push fires.
	wakeup("dev-99", "apns-token-1", "apns")
	select {
	case got := <-fake.calls:
		if got != [2]string{"apns", "apns-token-1"} {
			t.Fatalf("pushed %v, want [apns apns-token-1]", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pusher never called for an offline device")
	}

	// Pair a phone, then wake its id: connected → no push, and the phone must
	// not receive the wakeup frame either.
	phone, err := client.DialPhone(wsURL(srv), "tok-w1")
	if err != nil {
		t.Fatal(err)
	}
	defer phone.Close()
	devID := awaitReady(t, phone)
	awaitReady(t, host)

	wakeup(devID, "apns-token-2", "apns")
	select {
	case got := <-fake.calls:
		t.Fatalf("pushed %v for a connected device", got)
	case m, ok := <-phone.Recv():
		if ok {
			t.Fatalf("phone received %v — wakeup must never be forwarded", m)
		}
	case <-time.After(500 * time.Millisecond):
	}

	// Bad payload: dropped without a push.
	if err := host.WriteRawFrame(wire.Frame{Type: wire.TypeWakeup, Device: "dev-99", Payload: []byte("{")}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-fake.calls:
		t.Fatalf("pushed %v for a garbage payload", got)
	case <-time.After(300 * time.Millisecond):
	}
}
