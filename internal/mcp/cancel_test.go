package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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

func (s *stallServer) expectCancel(t *testing.T) CancelledParams {
	t.Helper()
	select {
	case p := <-s.cancelled:
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("no notifications/cancelled reached the server")
		return CancelledParams{}
	}
}

func (s *stallServer) expectNoCancel(t *testing.T) {
	t.Helper()
	select {
	case p := <-s.cancelled:
		t.Fatalf("unexpected notifications/cancelled: %+v", p)
	case <-time.After(200 * time.Millisecond):
	}
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
	id := <-s.calls
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("CallTool err = %v, want context.Canceled", err)
	}

	p := s.expectCancel(t)
	if string(p.RequestID) != id {
		t.Errorf("requestId = %s, want the tools/call id %s", p.RequestID, id)
	}
	if p.Reason != context.Canceled.Error() {
		t.Errorf("reason = %q", p.Reason)
	}
}

func TestCallToolProgress_IdleTimeoutSendsCancelled(t *testing.T) {
	old := defaultCallTimeout
	defaultCallTimeout = 50 * time.Millisecond
	defer func() { defaultCallTimeout = old }()

	s, c := startStallServer(t, false)
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CallToolProgress(context.Background(), "long", nil, func(ProgressParams) {}); !errors.Is(err, errProgressIdle) {
		t.Fatalf("err = %v, want the idle timeout", err)
	}
	id := <-s.calls
	p := s.expectCancel(t)
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
	s.expectNoCancel(t)
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
	s.expectNoCancel(t)
}

// Over Streamable HTTP an interrupt drops the response stream, which the
// spec says is not a cancellation; the explicit notification must still be
// POSTed so the server stops the tool.
func TestCall_InterruptMidStreamCancelsOverHTTP(t *testing.T) {
	streaming := make(chan string, 1)
	cancelled := make(chan CancelledParams, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var in Message
		_ = json.NewDecoder(r.Body).Decode(&in)
		switch in.Method {
		case MethodInitialize:
			writeJSONRPC(w, in.ID, InitializeResult{
				ProtocolVersion: ProtocolVersion,
				Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				ServerInfo:      Implementation{Name: "vm"},
			})
		case MethodToolsCall:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			streaming <- string(in.ID)
			<-r.Context().Done() // the client hangs up
		case MethodCancelled:
			var p CancelledParams
			_ = json.Unmarshal(in.Params, &p)
			cancelled <- p
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()
	c := dialHTTP(t, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := c.CallTool(ctx, "run", nil)
		errc <- err
	}()
	id := <-streaming
	cancel()
	if err := <-errc; err == nil {
		t.Fatal("interrupted call succeeded")
	}
	select {
	case p := <-cancelled:
		if string(p.RequestID) != id {
			t.Errorf("requestId = %s, want %s", p.RequestID, id)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no notifications/cancelled for request %s", id)
	}
}
