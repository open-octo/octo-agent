package relay_test

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/cmd/octo-relay/internal/client"
	"github.com/open-octo/octo-agent/cmd/octo-relay/internal/relay"
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
