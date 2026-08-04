package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHTTPTransport_RoundtripWithSessionHeader(t *testing.T) {
	// Server echoes back a canned "result". On the first request it also
	// sets Mcp-Session-Id; subsequent requests must echo it back.
	var seenSession string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var in Message
		_ = json.Unmarshal(body, &in)

		// Record / verify the session id round-trip.
		if seenSession == "" {
			w.Header().Set("Mcp-Session-Id", "session-42")
			seenSession = "session-42"
		} else if got := r.Header.Get("Mcp-Session-Id"); got != seenSession {
			t.Errorf("expected session header %q, got %q", seenSession, got)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Reply matches the request id.
		_ = json.NewEncoder(w).Encode(Message{
			JSONRPC: "2.0",
			ID:      in.ID,
			Result:  json.RawMessage(`{"ok":true}`),
		})
	}))
	defer srv.Close()

	tx, err := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// First request — server should set the session id.
	req1 := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	if err := tx.Send(ctx, req1); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Receive(ctx); err != nil {
		t.Fatal(err)
	}

	// Second request — client should echo session id; the server above
	// asserts on a mismatch.
	req2 := &Message{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "tools/list"}
	if err := tx.Send(ctx, req2); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Receive(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPTransport_SSEResponse(t *testing.T) {
	// Server streams the response as a single SSE "data:" event instead of
	// a plain application/json body.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var in Message
		_ = json.Unmarshal(body, &in)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		resp, _ := json.Marshal(Message{JSONRPC: "2.0", ID: in.ID, Result: json.RawMessage(`{"ok":true}`)})
		fmt.Fprintf(w, "data: %s\n\n", resp)
	}))
	defer srv.Close()

	tx, err := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	if err := tx.Send(ctx, req); err != nil {
		t.Fatal(err)
	}
	got, err := tx.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Result) != `{"ok":true}` {
		t.Errorf("result = %s, want {\"ok\":true}", got.Result)
	}
}

// TestHTTPTransport_SSEResponse_InterleavedNotification covers a server that
// emits a notification frame ahead of the actual response on the same SSE
// stream — both must be queued, in order, and reading must stop once the
// frame matching our request id arrives rather than wait for stream close.
func TestHTTPTransport_SSEResponse_InterleavedNotification(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var in Message
		_ = json.Unmarshal(body, &in)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		notif, _ := json.Marshal(Message{JSONRPC: "2.0", Method: "notifications/progress"})
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", notif)
		resp, _ := json.Marshal(Message{JSONRPC: "2.0", ID: in.ID, Result: json.RawMessage(`{"ok":true}`)})
		fmt.Fprintf(w, "data: %s\n\n", resp)
	}))
	defer srv.Close()

	tx, err := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	if err := tx.Send(ctx, req); err != nil {
		t.Fatal(err)
	}

	first, err := tx.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.Method != "notifications/progress" {
		t.Errorf("first queued frame method = %q, want notifications/progress", first.Method)
	}

	second, err := tx.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !second.IsResponse() || string(second.Result) != `{"ok":true}` {
		t.Errorf("second queued frame = %+v, want the tools/list response", second)
	}
}

// TestHTTPTransport_SSEResponse_MultiLineData covers the SSE spec's
// multi-"data:" line form — several lines joined with "\n" reconstitute the
// original (here, pretty-printed) JSON text.
func TestHTTPTransport_SSEResponse_MultiLineData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var in Message
		_ = json.Unmarshal(body, &in)

		resp, _ := json.MarshalIndent(Message{
			JSONRPC: "2.0",
			ID:      in.ID,
			Result:  json.RawMessage(`{"ok":true}`),
		}, "", "  ")

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, line := range strings.Split(string(resp), "\n") {
			fmt.Fprintf(w, "data: %s\n", line)
		}
		fmt.Fprint(w, "\n")
	}))
	defer srv.Close()

	tx, err := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	if err := tx.Send(ctx, req); err != nil {
		t.Fatal(err)
	}
	got, err := tx.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// MarshalIndent reformats the whole document, including nested raw
	// messages, so compare parsed content rather than the exact bytes.
	var out struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(got.Result, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !out.OK {
		t.Errorf("result = %s, want ok:true", got.Result)
	}
}

// TestHTTPTransport_SSEResponse_MalformedFrame ensures a data: line that
// isn't valid JSON surfaces a decode error rather than being swallowed.
func TestHTTPTransport_SSEResponse_MalformedFrame(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: not-json\n\n")
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	defer tx.Close()

	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	err := tx.Send(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "decode sse frame") {
		t.Errorf("Send error = %v, want a decode sse frame error", err)
	}
}

// TestHTTPTransport_SSEResponse_ClosedWithoutAnswer covers a server that
// closes the SSE stream (e.g. after an unrelated notification) without ever
// sending the frame that answers our request — this must surface as an
// error, not hang until the caller's context timeout.
func TestHTTPTransport_SSEResponse_ClosedWithoutAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		notif, _ := json.Marshal(Message{JSONRPC: "2.0", Method: "notifications/progress"})
		fmt.Fprintf(w, "data: %s\n\n", notif)
		// Stream ends here — no response frame for our request id.
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	defer tx.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	err := tx.Send(ctx, req)
	if err == nil || !strings.Contains(err.Error(), "closed without a response") {
		t.Errorf("Send error = %v, want a closed-without-a-response error", err)
	}
}

// TestHTTPTransport_SSEResponse_EventSizeCapped guards the fix for a server
// that never emits the blank line ending an event: without a cumulative
// bound, readSSE's per-event buffer would grow without limit. Shrinks
// maxSSEEventBytes (a var for exactly this reason) so the test doesn't need
// to push real megabytes over the wire.
func TestHTTPTransport_SSEResponse_EventSizeCapped(t *testing.T) {
	orig := maxSSEEventBytes
	maxSSEEventBytes = 64
	defer func() { maxSSEEventBytes = orig }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 10; i++ {
			fmt.Fprintf(w, "data: %s\n", strings.Repeat("x", 20))
			if flusher != nil {
				flusher.Flush()
			}
		}
		// Deliberately never sends the blank line that would end the event.
	}))
	defer srv.Close()

	tx, err := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	err = tx.Send(ctx, req)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("Send error = %v, want an event-size-exceeds error", err)
	}
}

func TestHTTPTransport_ErrorStatusSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("kaboom"))
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	defer tx.Close()
	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	err := tx.Send(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for non-2xx response")
	}
}

func TestHTTPTransport_HeadersPassedThrough(t *testing.T) {
	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Message{
			JSONRPC: "2.0",
			ID:      json.RawMessage(`1`),
			Result:  json.RawMessage(`{}`),
		})
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{
		URL:     srv.URL,
		Headers: map[string]string{"Authorization": "Bearer abc"},
	})
	defer tx.Close()

	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	if err := tx.Send(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Receive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seenAuth != "Bearer abc" {
		t.Errorf("Authorization header = %q, want Bearer abc", seenAuth)
	}
}

func TestHTTPTransport_NotificationGet204(t *testing.T) {
	// Server returns 204 No Content — this is the path for an accepted
	// notification (no body to parse).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	defer tx.Close()

	notif := &Message{JSONRPC: "2.0", Method: "notifications/initialized"}
	if err := tx.Send(context.Background(), notif); err != nil {
		t.Errorf("notification Send returned error: %v", err)
	}
}

// TestHTTPTransport_NotificationEmptyBody200 covers a server that
// acknowledges a notification with 200 (not the spec-preferred 202, and not
// the 204 the previous test covers) and an empty body: since Notify never
// expects a response frame, this must not be treated as a JSON decode
// failure (an empty body isn't valid JSON).
func TestHTTPTransport_NotificationEmptyBody200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	defer tx.Close()

	notif := &Message{JSONRPC: "2.0", Method: "notifications/initialized"}
	if err := tx.Send(context.Background(), notif); err != nil {
		t.Errorf("notification Send returned error: %v", err)
	}
}

// TestHTTPTransport_ProtocolVersionHeader: once the handshake settles on a
// revision, every later request must advertise it — servers implementing
// 2025-03-26 or later reject requests that don't.
func TestHTTPTransport_ProtocolVersionHeader(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("MCP-Protocol-Version"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Message{
			JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(`{}`),
		})
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	defer tx.Close()
	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}

	// Before the handshake there is no negotiated version to send.
	if err := tx.Send(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Receive(context.Background()); err != nil {
		t.Fatal(err)
	}

	tx.SetProtocolVersion("2025-03-26")
	if err := tx.Send(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Receive(context.Background()); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(seen))
	}
	if seen[0] != "" {
		t.Errorf("pre-handshake request carried version %q, want none", seen[0])
	}
	if seen[1] != "2025-03-26" {
		t.Errorf("post-handshake request carried %q, want 2025-03-26", seen[1])
	}
}

// TestHTTPTransport_CloseDeletesSession: Close releases a server-issued
// session so the server can drop its state instead of holding it to timeout.
func TestHTTPTransport_CloseDeletesSession(t *testing.T) {
	type call struct{ method, session, version string }
	var mu sync.Mutex
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, call{r.Method, r.Header.Get("Mcp-Session-Id"), r.Header.Get("MCP-Protocol-Version")})
		mu.Unlock()
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Mcp-Session-Id", "sess-9")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Message{
			JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(`{}`),
		})
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	tx.SetProtocolVersion("2025-03-26")
	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	if err := tx.Send(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Receive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("calls = %+v, want a POST then a DELETE", calls)
	}
	del := calls[1]
	if del.method != http.MethodDelete {
		t.Errorf("second call method = %s, want DELETE", del.method)
	}
	if del.session != "sess-9" {
		t.Errorf("DELETE session = %q, want sess-9", del.session)
	}
	if del.version != "2025-03-26" {
		t.Errorf("DELETE version header = %q, want 2025-03-26", del.version)
	}
}

// TestHTTPTransport_CloseWithoutSessionSkipsDelete: no session id means there
// is nothing to release, so Close must not fire a stray DELETE.
func TestHTTPTransport_CloseWithoutSessionSkipsDelete(t *testing.T) {
	var mu sync.Mutex
	deletes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			mu.Lock()
			deletes++
			mu.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	notif := &Message{JSONRPC: "2.0", Method: "notifications/initialized"}
	if err := tx.Send(context.Background(), notif); err != nil {
		t.Fatal(err)
	}
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if deletes != 0 {
		t.Errorf("fired %d DELETEs without a session id, want 0", deletes)
	}
}

// TestHTTPTransport_CloseSurvivesDeleteFailure: the DELETE is a courtesy on a
// teardown path — an unreachable or hostile server must not make Close hang
// or fail.
func TestHTTPTransport_CloseSurvivesDeleteFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Mcp-Session-Id", "sess-x")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Message{
			JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(`{}`),
		})
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	if err := tx.Send(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Receive(context.Background()); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- tx.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Close returned %v, want nil despite the failed DELETE", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung on a failing DELETE")
	}
}

func TestNewHTTPTransport_EmptyURLRejected(t *testing.T) {
	_, err := NewHTTPTransport(HTTPConfig{})
	if err == nil {
		t.Error("expected error for empty URL")
	}
}

// TestHTTPTransport_CloseDoesNotCloseInbox guards the panic fix: Close must not
// close the inbox channel, because an in-flight doRequest may still be about to
// send on it (a send on a closed channel panics and crashes the process). The
// fix closes a separate done channel instead. Sending on inbox after Close must
// therefore NOT panic.
func TestHTTPTransport_CloseDoesNotCloseInbox(t *testing.T) {
	tr, err := NewHTTPTransport(HTTPConfig{URL: "http://example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	_ = tr.Close()
	_ = tr.Close() // idempotent — must not panic

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("inbox was closed by Close(); send panicked: %v", r)
		}
	}()
	tr.inbox <- &Message{} // buffered; would panic if Close had closed it
}

// TestHTTPTransport_ReceiveAfterCloseEOF: a pending Receive is unblocked with
// io.EOF after Close (via the done channel), preserving the prior contract.
func TestHTTPTransport_ReceiveAfterCloseEOF(t *testing.T) {
	tr, err := NewHTTPTransport(HTTPConfig{URL: "http://example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	_ = tr.Close()
	if _, err := tr.Receive(context.Background()); err != io.EOF {
		t.Fatalf("Receive after Close = %v, want io.EOF", err)
	}
	if err := tr.Send(context.Background(), &Message{}); err == nil {
		t.Fatal("Send after Close should return an error")
	}
}
