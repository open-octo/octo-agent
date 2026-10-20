package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func dialHTTP(t *testing.T, url string) *Client {
	t.Helper()
	tr, err := NewHTTPTransport(HTTPConfig{URL: url})
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
	return c
}

// A tool call that streams for a long time must not hold up other requests
// to the same server: the send slot covers writing a request and reading its
// headers, not the stream behind them.
func TestStreamingCallDoesNotBlockOtherRequests(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var in Message
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		switch in.Method {
		case MethodInitialize:
			writeJSONRPC(w, in.ID, InitializeResult{
				ProtocolVersion: ProtocolVersion,
				Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				ServerInfo:      Implementation{Name: "slow"},
			})
		case MethodToolsCall:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-release
			b, _ := json.Marshal(Message{JSONRPC: "2.0", ID: in.ID, Result: json.RawMessage(`{"content":[{"type":"text","text":"done"}]}`)})
			fmt.Fprintf(w, "data: %s\n\n", b)
		case MethodToolsList:
			writeJSONRPC(w, in.ID, ListToolsResult{Tools: []Tool{{Name: "slow"}}})
		}
	}))
	defer srv.Close()
	defer close(release)

	c := dialHTTP(t, srv.URL)

	callDone := make(chan error, 1)
	go func() {
		_, err := c.CallTool(context.Background(), "slow", nil)
		callDone <- err
	}()
	// Let the call's stream open before the second request goes out.
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := c.ListTools(ctx); err != nil {
		t.Fatalf("ListTools while a call streams: %v", err)
	}
	select {
	case err := <-callDone:
		t.Fatalf("the streaming call ended early: %v", err)
	default:
	}
}

// Several streams cut short by the same expired session recover it with one
// handshake, not one each.
func TestConcurrentMidStreamExpiryRecoversOnce(t *testing.T) {
	var mu sync.Mutex
	sessions := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound) // the resume: session gone
			return
		}
		var in Message
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		mu.Lock()
		if in.Method == MethodInitialize {
			sessions++
		}
		n := sessions
		mu.Unlock()
		switch {
		case in.Method == MethodInitialize:
			w.Header().Set("Mcp-Session-Id", fmt.Sprintf("sess-%d", n))
			writeJSONRPC(w, in.ID, InitializeResult{
				ProtocolVersion: ProtocolVersion,
				Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				ServerInfo:      Implementation{Name: "expiring"},
			})
		case n == 1:
			// First session: a tagged event, then hang up — a stream cut
			// short by the session expiring.
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "id: ev-1\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\"}\n\n")
			w.(http.Flusher).Flush()
			time.Sleep(50 * time.Millisecond) // both streams are open before either breaks
		default:
			writeJSONRPC(w, in.ID, CallToolResult{Content: []Content{{Type: "text", Text: "recovered"}}})
		}
	}))
	defer srv.Close()

	c := dialHTTP(t, srv.URL)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := c.CallTool(ctx, "x", nil)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if sessions != 2 {
		t.Errorf("initialize ran %d times; want the original plus one recovery", sessions)
	}
}
