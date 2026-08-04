package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSSEResume_PicksUpAfterBrokenStream: the POST stream dies after a tagged
// notification but before the response. The client must resume with a GET
// carrying Last-Event-ID and get its answer from the replay.
func TestSSEResume_PicksUpAfterBrokenStream(t *testing.T) {
	var mu sync.Mutex
	var lastEventID string
	var gets int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			mu.Lock()
			gets++
			lastEventID = r.Header.Get("Last-Event-ID")
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			// Replay what came after event 1: the response.
			resp, _ := json.Marshal(Message{
				JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(`{"resumed":true}`),
			})
			fmt.Fprintf(w, "id: 2\ndata: %s\n\n", resp)
			return
		}
		// POST: emit one tagged notification, then hang up mid-stream.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		notif, _ := json.Marshal(Message{JSONRPC: "2.0", Method: "notifications/progress"})
		fmt.Fprintf(w, "id: 1\ndata: %s\n\n", notif)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Returning here closes the body without ever sending the response.
	}))
	defer srv.Close()

	tx, err := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	if err := tx.Send(ctx, req); err != nil {
		t.Fatalf("Send should have recovered via resume, got: %v", err)
	}

	// The notification from the first stream, then the resumed response.
	first, err := tx.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.Method != "notifications/progress" {
		t.Errorf("first frame = %+v, want the pre-break notification", first)
	}
	second, err := tx.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(second.Result) != `{"resumed":true}` {
		t.Errorf("second frame result = %s, want the resumed response", second.Result)
	}

	mu.Lock()
	defer mu.Unlock()
	if gets != 1 {
		t.Errorf("resume GETs = %d, want 1", gets)
	}
	if lastEventID != "1" {
		t.Errorf("Last-Event-ID = %q, want 1 (the last event before the break)", lastEventID)
	}
}

// TestSSEResume_UntaggedStreamDoesNotResume: without event ids the server
// never opted into resumability and couldn't know where to restart, so the
// original failure must stand instead of firing a pointless GET.
func TestSSEResume_UntaggedStreamDoesNotResume(t *testing.T) {
	var mu sync.Mutex
	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			mu.Lock()
			gets++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		notif, _ := json.Marshal(Message{JSONRPC: "2.0", Method: "notifications/progress"})
		fmt.Fprintf(w, "data: %s\n\n", notif) // no id: — not resumable
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	defer tx.Close()
	err := tx.Send(context.Background(), &Message{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list",
	})
	if err == nil {
		t.Fatal("expected an error when an untagged stream ends without the response")
	}
	if !strings.Contains(err.Error(), "without a response") {
		t.Errorf("err = %v, want the stream-incomplete error", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gets != 0 {
		t.Errorf("fired %d resume GETs for an untagged stream, want 0", gets)
	}
}

// TestSSEResume_StopsWithoutForwardProgress: a server that replays the same
// event id forever must not be retried forever.
func TestSSEResume_StopsWithoutForwardProgress(t *testing.T) {
	var mu sync.Mutex
	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			mu.Lock()
			gets++
			mu.Unlock()
		}
		// Always the same tagged notification, never the response.
		notif, _ := json.Marshal(Message{JSONRPC: "2.0", Method: "notifications/progress"})
		fmt.Fprintf(w, "id: same\ndata: %s\n\n", notif)
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	defer tx.Close()

	done := make(chan error, 1)
	go func() {
		done <- tx.Send(context.Background(), &Message{
			JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list",
		})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error; the response never arrived")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Send never returned — resume is looping")
	}
	mu.Lock()
	defer mu.Unlock()
	// First resume sees "same" again → no forward progress → stop. One GET.
	if gets > maxSSEResumeAttempts {
		t.Errorf("resume GETs = %d, want at most %d", gets, maxSSEResumeAttempts)
	}
}

// TestSSEResume_ServerWithoutGETReportsOriginalFailure: a server answering 405
// to the resume GET has no replay endpoint. The error the caller sees should
// describe the stream that actually broke, not the missing GET.
func TestSSEResume_ServerWithoutGETReportsOriginalFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		notif, _ := json.Marshal(Message{JSONRPC: "2.0", Method: "notifications/progress"})
		fmt.Fprintf(w, "id: 7\ndata: %s\n\n", notif)
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
	if !strings.Contains(err.Error(), "without a response") {
		t.Errorf("err = %v, want the original stream failure to lead", err)
	}
	if !strings.Contains(err.Error(), "405") {
		t.Errorf("err = %v, want the resume attempt's 405 as context", err)
	}
}

// TestSSEResume_CarriesSessionAndVersion: the resume GET is a normal MCP
// request and must identify itself the same way a POST does, or a server that
// enforces either header will refuse to replay.
func TestSSEResume_CarriesSessionAndVersion(t *testing.T) {
	var mu sync.Mutex
	var gotSession, gotVersion, gotAccept string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			mu.Lock()
			gotSession = r.Header.Get("Mcp-Session-Id")
			gotVersion = r.Header.Get("MCP-Protocol-Version")
			gotAccept = r.Header.Get("Accept")
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			resp, _ := json.Marshal(Message{
				JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(`{}`),
			})
			fmt.Fprintf(w, "id: 2\ndata: %s\n\n", resp)
			return
		}
		w.Header().Set("Mcp-Session-Id", "sess-resume")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		notif, _ := json.Marshal(Message{JSONRPC: "2.0", Method: "notifications/progress"})
		fmt.Fprintf(w, "id: 1\ndata: %s\n\n", notif)
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	defer tx.Close()
	tx.SetProtocolVersion("2025-03-26")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tx.Send(ctx, &Message{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list",
	}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotSession != "sess-resume" {
		t.Errorf("resume GET session = %q, want sess-resume", gotSession)
	}
	if gotVersion != "2025-03-26" {
		t.Errorf("resume GET version = %q, want 2025-03-26", gotVersion)
	}
	if !strings.Contains(gotAccept, "text/event-stream") {
		t.Errorf("resume GET Accept = %q, want it to accept an event stream", gotAccept)
	}
}
