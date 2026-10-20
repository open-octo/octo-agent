package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recv waits for one value from ch, failing the test instead of hanging when
// nothing arrives.
func recv[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

// expectNone fails if ch delivers anything within a short window.
func expectNone[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()
	select {
	case v := <-ch:
		t.Fatalf("unexpected %s: %+v", what, v)
	case <-time.After(200 * time.Millisecond):
	}
}

// stallServer answers initialize (unless stallInit), never answers a
// tools/call, and reports every notifications/cancelled it receives on
// cancelled — together with the ids of the tools/call requests it saw.
type stallServer struct {
	tx        *mockTransport
	stallInit bool
	calls     chan string
	cancelled chan CancelledParams
	stop      chan struct{}
	done      chan struct{}
}

func startStallServer(t *testing.T, stallInit bool) (*stallServer, *Client) {
	t.Helper()
	s := &stallServer{
		tx:        newMockTransport(),
		stallInit: stallInit,
		calls:     make(chan string, 4),
		cancelled: make(chan CancelledParams, 4),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	go func() {
		defer close(s.done)
		for {
			var req *Message
			select {
			case req = <-s.tx.in:
			case <-s.stop:
				return
			}
			switch {
			case req.Method == MethodCancelled:
				var p CancelledParams
				_ = json.Unmarshal(req.Params, &p)
				s.cancelled <- p
			case req.Method == MethodInitialize && !s.stallInit:
				b, _ := json.Marshal(InitializeResult{ProtocolVersion: ProtocolVersion, ServerInfo: Implementation{Name: "stall"}})
				select {
				case s.tx.out <- &Message{JSONRPC: "2.0", ID: req.ID, Result: b}:
				case <-s.stop:
					return
				}
			case req.Method == MethodToolsCall:
				s.calls <- string(req.ID)
			}
		}
	}()
	c := NewClient(s.tx, Implementation{Name: "test"})
	t.Cleanup(func() {
		close(s.stop)
		<-s.done
		_ = c.Close()
	})
	return s, c
}

func TestCall_InterruptSendsCancelled(t *testing.T) {
	s, c := startStallServer(t, false)
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := c.CallTool(ctx, "long", nil)
		errc <- err
	}()
	id := recv(t, s.calls, "the tools/call")
	cancel()
	if err := recv(t, errc, "CallTool to return"); !errors.Is(err, context.Canceled) {
		t.Fatalf("CallTool err = %v, want context.Canceled", err)
	}

	p := recv(t, s.cancelled, "notifications/cancelled")
	if string(p.RequestID) != id {
		t.Errorf("requestId = %s, want the tools/call id %s", p.RequestID, id)
	}
	if p.Reason != context.Canceled.Error() {
		t.Errorf("reason = %q", p.Reason)
	}
}

func TestCallToolProgress_IdleTimeoutSendsCancelled(t *testing.T) {
	s, c := startStallServer(t, false)
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	old := defaultCallTimeout
	defaultCallTimeout = 50 * time.Millisecond
	defer func() { defaultCallTimeout = old }()

	if _, err := c.CallToolProgress(context.Background(), "long", nil, func(ProgressParams) {}); !errors.Is(err, errProgressIdle) {
		t.Fatalf("err = %v, want the idle timeout", err)
	}
	id := recv(t, s.calls, "the tools/call")
	p := recv(t, s.cancelled, "notifications/cancelled")
	if string(p.RequestID) != id {
		t.Errorf("requestId = %s, want %s", p.RequestID, id)
	}
	if !strings.Contains(p.Reason, errProgressIdle.Error()) {
		t.Errorf("reason = %q, want it to say why", p.Reason)
	}
}

func TestInitialize_TimeoutIsNeverCancelled(t *testing.T) {
	s, c := startStallServer(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.Initialize(ctx); err == nil {
		t.Fatal("Initialize succeeded against a server that never answers")
	}
	expectNone(t, s.cancelled, "notifications/cancelled")
}

func TestCall_AbandonedBeforeSendingIsNotCancelled(t *testing.T) {
	s, c := startStallServer(t, false)
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Hold the send slot so the call gives up before its request goes out:
	// the server never saw it, so there is nothing to cancel.
	c.sendSem <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.CallTool(ctx, "long", nil)
	// Free the slot before looking: a cancellation, if one were sent, needs it
	// to go out.
	<-c.sendSem
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	expectNone(t, s.cancelled, "notifications/cancelled")
}

// cancelSeen is one notifications/cancelled as an HTTP server received it.
type cancelSeen struct {
	params CancelledParams
	auth   string
}

// stallHTTPServer is a Streamable HTTP server whose tools/call never
// answers — either after opening an SSE stream, or (sse false) before
// sending any response headers, the way a plain-JSON server works through
// a call — until the client hangs up.
type stallHTTPServer struct {
	sse       bool
	cancel404 bool // answer the cancellation as if the session were gone

	inits     atomic.Int32
	calls     chan string
	cancelled chan cancelSeen
}

func newStallHTTPServer(t *testing.T, sse bool) (*stallHTTPServer, *httptest.Server) {
	t.Helper()
	s := &stallHTTPServer{sse: sse, calls: make(chan string, 4), cancelled: make(chan cancelSeen, 4)}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return s, srv
}

func (s *stallHTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var in Message
	_ = json.NewDecoder(r.Body).Decode(&in)
	switch in.Method {
	case MethodInitialize:
		s.inits.Add(1)
		w.Header().Set("Mcp-Session-Id", "sess-1")
		writeJSONRPC(w, in.ID, InitializeResult{
			ProtocolVersion: ProtocolVersion,
			Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
			ServerInfo:      Implementation{Name: "vm"},
		})
	case MethodToolsCall:
		if s.sse {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
		}
		s.calls <- string(in.ID)
		<-r.Context().Done() // the client hangs up
	case MethodCancelled:
		var p CancelledParams
		_ = json.Unmarshal(in.Params, &p)
		s.cancelled <- cancelSeen{p, r.Header.Get("Authorization")}
		if s.cancel404 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

func dialHTTPWith(t *testing.T, cfg HTTPConfig) (*Client, *HTTPTransport) {
	t.Helper()
	tr, err := NewHTTPTransport(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(tr, Implementation{Name: "test"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, tr
}

// interruptCall starts a tools/call, waits for the server to be working on
// it, interrupts it, and returns the request id.
func interruptCall(t *testing.T, c *Client, calls <-chan string) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := c.CallTool(ctx, "run", nil)
		errc <- err
	}()
	id := recv(t, calls, "the tools/call")
	cancel()
	if err := recv(t, errc, "CallTool to return"); err == nil {
		t.Fatal("interrupted call succeeded")
	}
	return id
}

// Over Streamable HTTP an interrupt drops the connection, which the spec says
// is not a cancellation; the explicit notification must still be POSTed so
// the server stops the tool — whether it answers calls with an SSE stream or
// works through them before sending any response at all.
func TestCall_InterruptCancelsOverHTTP(t *testing.T) {
	for _, sse := range []bool{true, false} {
		t.Run(map[bool]string{true: "sse", false: "plain-json"}[sse], func(t *testing.T) {
			s, srv := newStallHTTPServer(t, sse)
			c, _ := dialHTTPWith(t, HTTPConfig{URL: srv.URL})

			id := interruptCall(t, c, s.calls)
			got := recv(t, s.cancelled, "notifications/cancelled")
			if string(got.params.RequestID) != id {
				t.Errorf("requestId = %s, want %s", got.params.RequestID, id)
			}
			if got.params.Reason != context.Canceled.Error() {
				t.Errorf("reason = %q", got.params.Reason)
			}
		})
	}
}

// gateOAuth is an OAuth provider whose Token can be made to block until its
// caller gives up — standing in for a real one stuck on an authorization
// prompt — and which counts Token calls, since for a cancellation any call
// at all would be the escalation the test forbids.
type gateOAuth struct {
	mu     sync.Mutex
	tokens int
	cached string
	block  atomic.Bool
}

func (o *gateOAuth) Token(ctx context.Context) (string, error) {
	o.mu.Lock()
	o.tokens++
	o.mu.Unlock()
	if o.block.Load() {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return "live-token", nil
}

func (o *gateOAuth) Invalidate() {}

func (o *gateOAuth) CachedToken() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.cached
}

func (o *gateOAuth) tokenCalls() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.tokens
}

// A cancellation is sent with the cached token only. Token can escalate to a
// browser prompt, which must never happen for a send the user didn't ask for.
func TestCancel_UsesCachedTokenOnly(t *testing.T) {
	s, srv := newStallHTTPServer(t, true)
	auth := &gateOAuth{cached: "cached-token"}
	c, _ := dialHTTPWith(t, HTTPConfig{URL: srv.URL, OAuth: auth})

	interruptCall(t, c, s.calls)
	before := auth.tokenCalls()
	got := recv(t, s.cancelled, "notifications/cancelled")
	if got.auth != "Bearer cached-token" {
		t.Errorf("Authorization = %q, want the cached token", got.auth)
	}
	if after := auth.tokenCalls(); after != before {
		t.Errorf("the cancellation called Token %d time(s)", after-before)
	}
}

// With no cached token there is nothing safe to authenticate with, so the
// cancellation is dropped rather than escalated.
func TestCancel_WithoutCachedTokenIsDropped(t *testing.T) {
	s, srv := newStallHTTPServer(t, true)
	auth := &gateOAuth{}
	c, _ := dialHTTPWith(t, HTTPConfig{URL: srv.URL, OAuth: auth})

	interruptCall(t, c, s.calls)
	before := auth.tokenCalls()
	expectNone(t, s.cancelled, "notifications/cancelled")
	if after := auth.tokenCalls(); after != before {
		t.Errorf("Token called %d time(s) for a dropped cancellation", after-before)
	}
}

// Interrupted while still getting a token (e.g. at an authorization prompt),
// the request never went out: there is nothing to cancel.
func TestCancel_InterruptDuringTokenSendsNothing(t *testing.T) {
	s, srv := newStallHTTPServer(t, true)
	auth := &gateOAuth{cached: "cached-token"}
	c, _ := dialHTTPWith(t, HTTPConfig{URL: srv.URL, OAuth: auth})
	auth.block.Store(true)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.CallTool(ctx, "run", nil); err == nil {
		t.Fatal("call succeeded without a token")
	}
	expectNone(t, s.calls, "tools/call")
	expectNone(t, s.cancelled, "notifications/cancelled")
}

// A cancellation answered with 404 (the session died meanwhile) must neither
// re-handshake for it nor clear the session: with no recovery following,
// clearing it would send every later request out with no session id.
func TestCancel_404LeavesSessionAlone(t *testing.T) {
	s, srv := newStallHTTPServer(t, true)
	s.cancel404 = true
	c, tr := dialHTTPWith(t, HTTPConfig{URL: srv.URL})

	interruptCall(t, c, s.calls)
	recv(t, s.cancelled, "notifications/cancelled")
	time.Sleep(100 * time.Millisecond) // let the client take in the 404

	tr.sessionMu.Lock()
	sid := tr.sessionID
	tr.sessionMu.Unlock()
	if sid != "sess-1" {
		t.Errorf("session id = %q after a 404 to the cancellation, want it kept", sid)
	}
	if n := s.inits.Load(); n != 1 {
		t.Errorf("initialize ran %d times; a cancellation must not re-handshake", n)
	}
}
