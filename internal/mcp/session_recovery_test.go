package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// expiringServer models a server that forgets its session partway through:
// it hands out session ids, and once expireAfter tool calls have been served
// on a session it answers 404 to anything still carrying that id, forcing the
// client to start a new session.
type expiringServer struct {
	mu       sync.Mutex
	nextID   int
	live     map[string]bool // session id -> still valid
	calls    int             // tools/call requests served
	expireAt int             // expire the current session after this many calls
	handshak int             // initialize requests seen
	sessions []string        // session id sent with each request, in order
}

func newExpiringServer(expireAfter int) *expiringServer {
	return &expiringServer{live: map[string]bool{}, expireAt: expireAfter}
}

func (s *expiringServer) handshakes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handshak
}

func (s *expiringServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var in Message
		_ = json.NewDecoder(r.Body).Decode(&in)
		sent := r.Header.Get("Mcp-Session-Id")

		s.mu.Lock()
		s.sessions = append(s.sessions, sent)
		// A request on a session we've retired: 404, per the spec's
		// session-termination signal.
		if sent != "" && !s.live[sent] {
			s.mu.Unlock()
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("session not found"))
			return
		}
		isInit := in.Method == MethodInitialize
		if isInit {
			s.handshak++
			s.nextID++
			sid := "sess-" + string(rune('a'+s.nextID))
			s.live[sid] = true
			s.calls = 0
			s.mu.Unlock()
			w.Header().Set("Mcp-Session-Id", sid)
			writeJSONRPC(w, in.ID, InitializeResult{
				ProtocolVersion: ProtocolVersion,
				Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				ServerInfo:      Implementation{Name: "expiring", Version: "1"},
			})
			return
		}
		if in.ID == nil { // notification
			s.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if in.Method == MethodToolsCall {
			s.calls++
			if s.expireAt > 0 && s.calls > s.expireAt {
				delete(s.live, sent) // retire it, then answer this one with 404
				s.mu.Unlock()
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte("session expired"))
				return
			}
		}
		s.mu.Unlock()
		switch in.Method {
		case MethodToolsList:
			writeJSONRPC(w, in.ID, ListToolsResult{Tools: []Tool{{Name: "ping"}}})
		case MethodToolsCall:
			writeJSONRPC(w, in.ID, CallToolResult{Content: []Content{{Type: "text", Text: "pong"}}})
		default:
			writeJSONRPC(w, in.ID, map[string]any{})
		}
	}
}

func writeJSONRPC(w http.ResponseWriter, id json.RawMessage, result any) {
	raw, _ := json.Marshal(result)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Message{JSONRPC: "2.0", ID: id, Result: raw})
}

// TestSessionExpiry_RecoversTransparently is the point of the feature: a call
// landing on a session the server has forgotten must re-handshake and succeed
// rather than surfacing an error the user has to fix by reconnecting by hand.
func TestSessionExpiry_RecoversTransparently(t *testing.T) {
	fake := newExpiringServer(1) // first tool call fine, second gets 404
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	tx, err := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(tx, Implementation{Name: "test", Version: "1"})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fake.handshakes(); got != 1 {
		t.Fatalf("handshakes after Initialize = %d, want 1", got)
	}

	if _, err := c.CallTool(ctx, "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// This one hits the retired session; recovery should be invisible here.
	res, err := c.CallTool(ctx, "ping", nil)
	if err != nil {
		t.Fatalf("call after session expiry should have recovered, got: %v", err)
	}
	if len(res.Content) == 0 || res.Content[0].Text != "pong" {
		t.Errorf("result = %+v, want the replayed call's pong", res.Content)
	}
	if got := fake.handshakes(); got != 2 {
		t.Errorf("handshakes = %d, want 2 (the original plus recovery)", got)
	}
}

// TestSessionExpiry_ReplayCarriesNewSession: the replayed request must go out
// on the session the recovery handshake established, not the dead one.
func TestSessionExpiry_ReplayCarriesNewSession(t *testing.T) {
	fake := newExpiringServer(1)
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	c := NewClient(tx, Implementation{Name: "test", Version: "1"})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CallTool(ctx, "ping", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CallTool(ctx, "ping", nil); err != nil {
		t.Fatal(err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	// The recovery initialize must carry no session id (we dropped the dead
	// one), and the final replay must carry the fresh one.
	var sawEmptyAfterFirst bool
	for i, s := range fake.sessions {
		if i > 0 && s == "" {
			sawEmptyAfterFirst = true
		}
	}
	if !sawEmptyAfterFirst {
		t.Errorf("no session-less request after the expiry; recovery handshake reused a dead id: %q", fake.sessions)
	}
	last := fake.sessions[len(fake.sessions)-1]
	if last == "" || !fake.live[last] {
		t.Errorf("replay went out on session %q, want the live post-recovery one (live=%v)", last, fake.live)
	}
}

// TestSessionExpiry_NoSessionMeansPlain404: a 404 on a request that never
// carried a session id is an ordinary wrong-endpoint error. Treating it as an
// expiry would hide a misconfigured URL behind a pointless retry.
func TestSessionExpiry_NoSessionMeansPlain404(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("no such endpoint"))
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	defer tx.Close()
	err := tx.Send(context.Background(), &Message{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list",
	})
	if err == nil {
		t.Fatal("expected an error for 404")
	}
	if errors.Is(err, ErrSessionExpired) {
		t.Errorf("err = %v, want a plain HTTP error not ErrSessionExpired", err)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v, want it to mention the status", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Errorf("made %d requests, want 1 (no retry for a plain 404)", requests)
	}
}

// TestSessionExpiry_GivesUpAfterOneRecovery: a server that expires every
// session immediately must produce an error, not an endless handshake loop.
func TestSessionExpiry_GivesUpAfterOneRecovery(t *testing.T) {
	// expireAt=0 with a live-session check that retires on first use: every
	// tool call 404s, including the one replayed after recovery.
	var mu sync.Mutex
	handshakes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in Message
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Method == MethodInitialize {
			mu.Lock()
			handshakes++
			mu.Unlock()
			w.Header().Set("Mcp-Session-Id", "always-stale")
			writeJSONRPC(w, in.ID, InitializeResult{
				ProtocolVersion: ProtocolVersion,
				Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				ServerInfo:      Implementation{Name: "hostile", Version: "1"},
			})
			return
		}
		if in.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		// Every real request claims the session is gone.
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("gone again"))
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	c := NewClient(tx, Implementation{Name: "test", Version: "1"})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := c.CallTool(ctx, "ping", nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error from a server that expires every session")
		}
		if !errors.Is(err, ErrSessionExpired) {
			t.Errorf("err = %v, want it to wrap ErrSessionExpired", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("CallTool never returned — recovery is looping")
	}
	mu.Lock()
	defer mu.Unlock()
	// One startup handshake plus exactly one recovery attempt.
	if handshakes != 2 {
		t.Errorf("handshakes = %d, want 2 (no retry storm)", handshakes)
	}
}

// TestSessionExpiry_ConcurrentReadersSeeNoRace exercises the metaMu guard:
// recovery rewrites the handshake metadata mid-session while other goroutines
// read it, which is a data race without the lock. Meaningful under -race.
func TestSessionExpiry_ConcurrentReadersSeeNoRace(t *testing.T) {
	fake := newExpiringServer(1)
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	c := NewClient(tx, Implementation{Name: "test", Version: "1"})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = c.Capabilities()
					_ = c.ServerInfo()
					_ = c.Instructions()
					_ = c.ProtocolVersion()
				}
			}
		}()
	}

	if _, err := c.CallTool(ctx, "ping", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CallTool(ctx, "ping", nil); err != nil { // triggers recovery
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()
}
