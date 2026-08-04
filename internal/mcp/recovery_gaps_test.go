package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// escalatingOAuth stands in for the real provider, whose Token() escalates
// through refresh to a full authorization — discovery, registration, and
// opening a browser — when the cache is stale. Here Token() just succeeds and
// counts, because what's under test is *who calls it*: a teardown path must
// read the cache instead, so any Token call from Close is the failure.
type escalatingOAuth struct {
	mu           sync.Mutex
	tokenCalls   int
	cachedCalls  int
	cachedResult string
}

func (o *escalatingOAuth) Token(context.Context) (string, error) {
	o.mu.Lock()
	o.tokenCalls++
	o.mu.Unlock()
	return "live-token", nil
}

func (o *escalatingOAuth) Invalidate() {}

func (o *escalatingOAuth) CachedToken() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cachedCalls++
	return o.cachedResult
}

func (o *escalatingOAuth) counts() (tokens, cached int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.tokenCalls, o.cachedCalls
}

// TestClose_NeverStartsInteractiveAuth: releasing a session on teardown must
// not reach for a token it would have to authorize for. Typing /exit should
// never open a browser.
func TestClose_NeverStartsInteractiveAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Mcp-Session-Id", "sess-bye")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Message{
			JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(`{}`),
		})
	}))
	defer srv.Close()

	oa := &escalatingOAuth{}
	tx, err := NewHTTPTransport(HTTPConfig{URL: srv.URL, OAuth: oa})
	if err != nil {
		t.Fatal(err)
	}
	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	if err := tx.Send(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Receive(context.Background()); err != nil {
		t.Fatal(err)
	}

	beforeTokens, _ := oa.counts()
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}
	afterTokens, cached := oa.counts()

	if afterTokens != beforeTokens {
		t.Errorf("Close called Token %d extra time(s); it must only read the cache",
			afterTokens-beforeTokens)
	}
	if cached == 0 {
		t.Error("Close never consulted CachedToken; the DELETE went out unauthenticated")
	}
}

// TestClose_UsesCachedTokenWhenAvailable: the goodbye is still authenticated
// when a usable token happens to be cached.
func TestClose_UsesCachedTokenWhenAvailable(t *testing.T) {
	var mu sync.Mutex
	var deleteAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			mu.Lock()
			deleteAuth = r.Header.Get("Authorization")
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Mcp-Session-Id", "sess-bye")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Message{
			JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(`{}`),
		})
	}))
	defer srv.Close()

	oa := &escalatingOAuth{cachedResult: "cached-abc"}
	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL, OAuth: oa})
	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	if err := tx.Send(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Receive(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = tx.Close()

	mu.Lock()
	defer mu.Unlock()
	if deleteAuth != "Bearer cached-abc" {
		t.Errorf("DELETE Authorization = %q, want the cached bearer token", deleteAuth)
	}
}

// TestSSEMidStreamExpiry_RecoversAndClearsSession is the gap the reviewers
// found empirically: the spec names an expiring session as the one sanctioned
// reason to close a response stream early, so the resume GET is a likely place
// to meet a 404 — and it used to be reported as "the stream broke" while the
// dead session id stayed installed.
func TestSSEMidStreamExpiry_RecoversAndClearsSession(t *testing.T) {
	var mu sync.Mutex
	sessions := 0
	var sawGet bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == http.MethodGet {
			// The resume attempt: session is gone.
			mu.Lock()
			sawGet = true
			mu.Unlock()
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("session gone"))
			return
		}
		var in Message
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if in.Method == MethodInitialize {
			mu.Lock()
			sessions++
			sid := fmt.Sprintf("sess-%d", sessions)
			mu.Unlock()
			w.Header().Set("Mcp-Session-Id", sid)
			writeJSONRPC(w, in.ID, InitializeResult{
				ProtocolVersion: ProtocolVersion,
				Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				ServerInfo:      Implementation{Name: "expiring-stream", Version: "1"},
			})
			return
		}
		mu.Lock()
		firstSession := sessions == 1
		mu.Unlock()
		if firstSession && in.Method == MethodToolsCall {
			// Tagged event, then hang up without the response — the shape of a
			// stream cut short by an expiring session.
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			notif, _ := json.Marshal(Message{JSONRPC: "2.0", Method: "notifications/progress"})
			fmt.Fprintf(w, "id: ev-1\ndata: %s\n\n", notif)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			return
		}
		switch in.Method {
		case MethodToolsCall:
			writeJSONRPC(w, in.ID, CallToolResult{Content: []Content{{Type: "text", Text: "recovered"}}})
		case MethodToolsList:
			writeJSONRPC(w, in.ID, ListToolsResult{Tools: []Tool{{Name: "ping"}}})
		default:
			writeJSONRPC(w, in.ID, map[string]any{})
		}
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

	// The call whose stream dies mid-flight. Recovery must make it succeed.
	res, err := c.CallTool(ctx, "ping", nil)
	if err != nil {
		t.Fatalf("call should have recovered from a mid-stream expiry, got: %v", err)
	}
	if len(res.Content) == 0 || res.Content[0].Text != "recovered" {
		t.Errorf("result = %+v, want the replayed call's result", res.Content)
	}

	mu.Lock()
	defer mu.Unlock()
	if !sawGet {
		t.Error("no resume GET was attempted")
	}
	if sessions != 2 {
		t.Errorf("handshakes = %d, want 2 (original plus recovery)", sessions)
	}
}

// TestSSEMidStreamExpiry_SurfacesTypedError checks the transport-level
// contract directly: the resume 404 becomes ErrSessionExpired and the dead
// session id is dropped, rather than an opaque stream failure that leaves it
// installed.
func TestSSEMidStreamExpiry_SurfacesTypedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("session gone"))
			return
		}
		w.Header().Set("Mcp-Session-Id", "sess-A")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		notif, _ := json.Marshal(Message{JSONRPC: "2.0", Method: "notifications/progress"})
		fmt.Fprintf(w, "id: ev-1\ndata: %s\n\n", notif)
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	defer tx.Close()
	tx.SetProtocolVersion("2025-03-26")

	err := tx.Send(context.Background(), &Message{
		JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "tools/call",
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, ErrSessionExpired) {
		t.Errorf("err = %v, want it to wrap ErrSessionExpired", err)
	}

	tx.sessionMu.Lock()
	sid := tx.sessionID
	tx.sessionMu.Unlock()
	if sid != "" {
		t.Errorf("session id = %q after an expiry, want it dropped", sid)
	}
}

// TestForgetSession_ClearsProtocolVersion: a revision is negotiated per
// session, and the recovery initialize is the request that renegotiates it.
// Carrying the old version header into it lets a server that restarted onto a
// different revision answer 400 instead of negotiating.
func TestForgetSession_ClearsProtocolVersion(t *testing.T) {
	var mu sync.Mutex
	var versions []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		versions = append(versions, r.Header.Get("MCP-Protocol-Version"))
		sent := r.Header.Get("Mcp-Session-Id")
		mu.Unlock()
		if sent == "dead-session" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Mcp-Session-Id", "dead-session")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Message{
			JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(`{}`),
		})
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	defer tx.Close()

	// First request establishes the session; then adopt a negotiated version.
	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	if err := tx.Send(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Receive(context.Background()); err != nil {
		t.Fatal(err)
	}
	tx.SetProtocolVersion("2025-06-18")

	// Second request carries the dead session and gets 404.
	if err := tx.Send(context.Background(), req); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("err = %v, want ErrSessionExpired", err)
	}

	tx.protoMu.Lock()
	pv := tx.protocolVersion
	tx.protoMu.Unlock()
	if pv != "" {
		t.Errorf("protocol version = %q after expiry, want it cleared so initialize renegotiates", pv)
	}

	// And a following request must not advertise the stale revision.
	_ = tx.Send(context.Background(), req)
	mu.Lock()
	defer mu.Unlock()
	last := versions[len(versions)-1]
	if last == "2025-06-18" {
		t.Errorf("post-expiry request still advertised %q", last)
	}
}

// TestForgetSession_IgnoresStaleClobber: a 404 for an old session must not
// wipe state a concurrent request has already replaced.
func TestForgetSession_IgnoresStaleClobber(t *testing.T) {
	tx, _ := NewHTTPTransport(HTTPConfig{URL: "http://example.invalid"})
	defer tx.Close()

	tx.sessionMu.Lock()
	tx.sessionID = "newer-session"
	tx.sessionMu.Unlock()
	tx.SetProtocolVersion("2025-03-26")

	tx.forgetSession("older-session") // late 404 for a session we've moved past

	tx.sessionMu.Lock()
	sid := tx.sessionID
	tx.sessionMu.Unlock()
	tx.protoMu.Lock()
	pv := tx.protocolVersion
	tx.protoMu.Unlock()

	if sid != "newer-session" {
		t.Errorf("session id = %q, want the newer session untouched", sid)
	}
	if pv != "2025-03-26" {
		t.Errorf("protocol version = %q, want it untouched", pv)
	}
}

// TestResume405StillReportsStreamFailure guards the distinction: a server with
// no GET endpoint is not an expiry, so the original stream failure must still
// lead and no session state should be dropped.
func TestResume405StillReportsStreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Mcp-Session-Id", "sess-keep")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		notif, _ := json.Marshal(Message{JSONRPC: "2.0", Method: "notifications/progress"})
		fmt.Fprintf(w, "id: ev-9\ndata: %s\n\n", notif)
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	defer tx.Close()
	err := tx.Send(context.Background(), &Message{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list",
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrSessionExpired) {
		t.Errorf("err = %v, want a stream failure not an expiry", err)
	}
	if !strings.Contains(err.Error(), "405") {
		t.Errorf("err = %v, want the 405 as context", err)
	}
	tx.sessionMu.Lock()
	sid := tx.sessionID
	tx.sessionMu.Unlock()
	if sid != "sess-keep" {
		t.Errorf("session id = %q, want it kept — a 405 is not an expiry", sid)
	}
}
