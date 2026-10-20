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
	streaming := make(chan struct{})
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
			close(streaming)
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
	<-streaming

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

// streamExpiryServer validates sessions the way a real Streamable HTTP server
// does: a request with no session id gets 400, one with a dead session id gets
// 404 (the signal that triggers recovery). Session 1's tool calls each open a
// stream with one tagged event, then — once streams of them are open —
// session 1 dies and every stream breaks, so each must resume and find it gone.
type streamExpiryServer struct {
	t       *testing.T
	streams int

	mu       sync.Mutex
	sessions int
	dead     map[string]bool
	open     int
	allOpen  chan struct{}
	noSessID int // requests that arrived with no session id

	// Optional, for tests that need to act inside the expiry window: hangUp,
	// when set, holds the streams open until closed; resumedDead is closed
	// once a resume has been told the session is gone.
	hangUp      chan struct{}
	resumedDead chan struct{}
	resumedOnce sync.Once
}

func (s *streamExpiryServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	sid := r.Header.Get("Mcp-Session-Id")
	var in Message
	if r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&in)
	}
	if in.Method == MethodInitialize {
		s.mu.Lock()
		s.sessions++
		id := fmt.Sprintf("sess-%d", s.sessions)
		s.mu.Unlock()
		w.Header().Set("Mcp-Session-Id", id)
		writeJSONRPC(w, in.ID, InitializeResult{
			ProtocolVersion: ProtocolVersion,
			Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
			ServerInfo:      Implementation{Name: "expiring"},
		})
		return
	}
	s.mu.Lock()
	if sid == "" {
		s.noSessID++
		s.mu.Unlock()
		http.Error(w, "missing session", http.StatusBadRequest)
		return
	}
	if s.dead[sid] {
		s.mu.Unlock()
		if r.Method == http.MethodGet && s.resumedDead != nil {
			defer s.resumedOnce.Do(func() { close(s.resumedDead) })
		}
		http.Error(w, "session gone", http.StatusNotFound)
		return
	}
	s.mu.Unlock()
	if r.Method == http.MethodGet {
		// A resume on a live session: nothing to replay from here.
		http.Error(w, "no such stream", http.StatusBadRequest)
		return
	}
	if in.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if sid == "sess-1" && in.Method == MethodToolsCall {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "id: ev-1\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\"}\n\n")
		w.(http.Flusher).Flush()
		s.mu.Lock()
		s.open++
		if s.open == s.streams {
			s.dead["sess-1"] = true
			close(s.allOpen)
		}
		s.mu.Unlock()
		<-s.allOpen
		if s.hangUp != nil {
			<-s.hangUp
		}
		return // hang up: the stream breaks with the session
	}
	writeJSONRPC(w, in.ID, CallToolResult{Content: []Content{{Type: "text", Text: "recovered on " + sid}}})
}

// Several streams cut short by the same expired session must all recover —
// none may go out with the session cleared — and share one handshake.
func TestConcurrentMidStreamExpiryRecoversOnce(t *testing.T) {
	const streams = 3
	s := &streamExpiryServer{t: t, streams: streams, dead: map[string]bool{}, allOpen: make(chan struct{})}
	srv := httptest.NewServer(s)
	defer srv.Close()

	c := dialHTTP(t, srv.URL)

	var wg sync.WaitGroup
	errs := make(chan error, streams)
	for i := 0; i < streams; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			res, err := c.CallTool(ctx, "x", nil)
			if err == nil && res.Content[0].Text != "recovered on sess-2" {
				err = fmt.Errorf("result %q", res.Content[0].Text)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("CallTool: %v", err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions != 2 {
		t.Errorf("initialize ran %d times; want the original plus one recovery", s.sessions)
	}
	if s.noSessID != 0 {
		t.Errorf("%d request(s) went out with no session id", s.noSessID)
	}
}

// A stream whose session died, and whose call is then abandoned before it can
// retake the send slot to recover, must not leave the connection without a
// session: the next call still has to find the dead session, get its 404 and
// recover, rather than go out with no session id and be rejected with nothing
// left to trigger recovery.
func TestAbandonedMidStreamExpiryLeavesConnectionRecoverable(t *testing.T) {
	s := &streamExpiryServer{
		t: t, streams: 1, dead: map[string]bool{}, allOpen: make(chan struct{}),
		hangUp: make(chan struct{}), resumedDead: make(chan struct{}),
	}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c := dialHTTP(t, srv.URL)

	ctxA, cancelA := context.WithCancel(context.Background())
	doneA := make(chan error, 1)
	go func() {
		_, err := c.CallTool(ctxA, "x", nil)
		doneA <- err
	}()

	// Occupy the slot once A's stream is open, so A finds its session dead
	// and then has to wait to recover — and is abandoned while waiting.
	<-s.allOpen
	c.sendSem <- struct{}{}
	close(s.hangUp)
	<-s.resumedDead
	time.Sleep(50 * time.Millisecond) // let A take in the 404 and queue for the slot
	cancelA()
	if err := <-doneA; err == nil {
		t.Fatal("abandoned call A succeeded")
	}
	<-c.sendSem

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := c.CallTool(ctx, "x", nil)
	if err != nil {
		t.Fatalf("next call after the abandoned recovery: %v", err)
	}
	if got := res.Content[0].Text; got != "recovered on sess-2" {
		t.Errorf("result = %q", got)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.noSessID != 0 {
		t.Errorf("%d request(s) went out with no session id", s.noSessID)
	}
}
