package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestRequest204IsRejected: 204 is a valid answer to a notification but a
// protocol violation in response to a request — the spec requires JSON or an
// SSE stream. Returning success there queued nothing, so the caller sat out
// its entire timeout waiting for a response that was never coming.
func TestRequest204IsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	defer tx.Close()

	err := tx.Send(context.Background(), &Message{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list",
	})
	if err == nil {
		t.Fatal("expected an error for 204 answering a request")
	}
	if !strings.Contains(err.Error(), "204") {
		t.Errorf("err = %v, want it to name the status", err)
	}

	// A notification still accepts 204 — that path must not regress.
	if err := tx.Send(context.Background(), &Message{
		JSONRPC: "2.0", Method: "notifications/initialized",
	}); err != nil {
		t.Errorf("204 to a notification returned %v, want nil", err)
	}
}

// TestRequest204DoesNotHangCall proves the point end to end: before this, the
// call burned its whole deadline. It must now fail promptly.
func TestRequest204DoesNotHangCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in Message
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Method == MethodInitialize {
			writeJSONRPC(w, in.ID, InitializeResult{
				ProtocolVersion: ProtocolVersion,
				Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				ServerInfo:      Implementation{Name: "rude", Version: "1"},
			})
			return
		}
		w.WriteHeader(http.StatusNoContent) // wrong for a request
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

	start := time.Now()
	_, err := c.CallTool(ctx, "ping", nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error")
	}
	if elapsed > 2*time.Second {
		t.Errorf("call took %v — it waited for a response that can't arrive", elapsed)
	}
}

// deadTransport delivers one Receive error and then blocks, modelling a stdio
// server that desynchronised its decoder or closed stdout while staying alive:
// writes still succeed, but nothing will ever demux a response again.
type deadTransport struct {
	mu    sync.Mutex
	sends int
	fail  chan struct{}
	done  chan struct{}
}

func newDeadTransport() *deadTransport {
	return &deadTransport{fail: make(chan struct{}), done: make(chan struct{})}
}

func (d *deadTransport) Send(_ context.Context, _ *Message) error {
	d.mu.Lock()
	d.sends++
	d.mu.Unlock()
	return nil // stdin is still writable
}

func (d *deadTransport) Receive(ctx context.Context) (*Message, error) {
	select {
	case <-d.fail:
		return nil, errors.New("invalid character 'x' looking for beginning of value")
	case <-d.done:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (d *deadTransport) Close() error {
	select {
	case <-d.done:
	default:
		close(d.done)
	}
	return nil
}

func (d *deadTransport) sendCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sends
}

// TestReceiveLoopDeath_FailsCallsFast: once the receive loop has exited there
// is nobody to match a response, so a call must fail immediately instead of
// waiting out its deadline. The old behaviour made a single malformed line
// from a stdio server cost 60s per call while the panel still said connected.
func TestReceiveLoopDeath_FailsCallsFast(t *testing.T) {
	prev := defaultCallTimeout
	defaultCallTimeout = 30 * time.Second // long enough that a hang is obvious
	defer func() { defaultCallTimeout = prev }()

	tx := newDeadTransport()
	c := NewClient(tx, Implementation{Name: "test", Version: "1"})
	defer c.Close()
	go c.receiveLoop()

	// Kill the reader, then wait for the loop to actually exit.
	close(tx.fail)
	select {
	case <-c.rxDone:
	case <-time.After(3 * time.Second):
		t.Fatal("receive loop never exited")
	}

	if c.Live() {
		t.Error("Live() is true after the receive loop died")
	}

	before := tx.sendCount()
	start := time.Now()
	err := c.Call(context.Background(), MethodToolsList, struct{}{}, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from a client whose reader has died")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Call took %v — it waited for a reply nobody can deliver", elapsed)
	}
	if !strings.Contains(err.Error(), "no longer readable") {
		t.Errorf("err = %v, want it to say the connection is unreadable", err)
	}
	// And it shouldn't have bothered writing.
	if after := tx.sendCount(); after != before {
		t.Errorf("Call still wrote to a dead connection (%d → %d sends)", before, after)
	}
}

// TestClose_AfterReceiveLoopDeath_StillClosesTransport guards the reason the
// dead flag is separate from closed: reusing closed would make Close's
// CompareAndSwap skip the teardown and leak the connection.
func TestClose_AfterReceiveLoopDeath_StillClosesTransport(t *testing.T) {
	tx := newDeadTransport()
	c := NewClient(tx, Implementation{Name: "test", Version: "1"})
	go c.receiveLoop()
	close(tx.fail)
	<-c.rxDone

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-tx.done:
	default:
		t.Error("Close did not tear the transport down after the receive loop had died")
	}
}

// TestSendSlot_ReleasedByCallerDeadline: a slow SSE response holds the write
// slot for the length of the stream. A caller queued behind it must be able to
// give up on its own deadline — with a plain Mutex it could only be released by
// the holder finishing, so neither its timeout nor an interrupt reached it.
func TestSendSlot_ReleasedByCallerDeadline(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in Message
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if in.Method == MethodInitialize {
			writeJSONRPC(w, in.ID, InitializeResult{
				ProtocolVersion: ProtocolVersion,
				Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				ServerInfo:      Implementation{Name: "slow-stream", Version: "1"},
			})
			return
		}
		// A stream that stays open, holding the send slot.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		notif, _ := json.Marshal(Message{JSONRPC: "2.0", Method: "notifications/progress"})
		fmt.Fprintf(w, "data: %s\n\n", notif)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
		res, _ := json.Marshal(CallToolResult{Content: []Content{{Type: "text", Text: "done"}}})
		resp, _ := json.Marshal(Message{JSONRPC: "2.0", ID: in.ID, Result: res})
		fmt.Fprintf(w, "data: %s\n\n", resp)
	}))
	defer srv.Close()
	defer once.Do(func() { close(release) })

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	c := NewClient(tx, Implementation{Name: "test", Version: "1"})
	defer c.Close()
	initCtx, cancelInit := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelInit()
	if err := c.Initialize(initCtx); err != nil {
		t.Fatal(err)
	}

	// Occupy the slot with the streaming call.
	holderDone := make(chan struct{})
	go func() {
		defer close(holderDone)
		hctx, hcancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer hcancel()
		_, _ = c.CallTool(hctx, "slow", nil)
	}()

	// Give the holder time to take the slot and start streaming.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		time.Sleep(300 * time.Millisecond)
	}()
	<-drained

	// A second caller with a short deadline must come back on its own.
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer shortCancel()
	start := time.Now()
	err := c.Call(shortCtx, MethodToolsList, struct{}{}, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("queued call should have failed on its own deadline")
	}
	if elapsed > 3*time.Second {
		t.Errorf("queued call took %v — it could not be released by its deadline", elapsed)
	}

	once.Do(func() { close(release) })
	select {
	case <-holderDone:
	case <-time.After(10 * time.Second):
		t.Error("holder never finished")
	}
}

// TestRegistryClose_DoesNotHoldLockAcrossIO: a connection whose Close blocks
// (a stdio grandchild keeping the stderr pipe open makes cmd.Wait hang even
// after a Kill) must not wedge every Registry reader with it.
func TestRegistryClose_DoesNotHoldLockAcrossIO(t *testing.T) {
	blocked := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			<-blocked // a server that never lets go of the session
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var in Message
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch in.Method {
		case MethodInitialize:
			result = InitializeResult{
				ProtocolVersion: ProtocolVersion,
				Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				ServerInfo:      Implementation{Name: "clingy", Version: "1"},
			}
		case MethodToolsList:
			result = map[string]any{"tools": []Tool{}}
		default:
			result = map[string]any{}
		}
		raw, _ := json.Marshal(result)
		w.Header().Set("Mcp-Session-Id", "sticky")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Message{JSONRPC: "2.0", ID: in.ID, Result: raw})
	}))
	defer srv.Close()
	// Registered after srv.Close so it runs BEFORE it: httptest.Close waits for
	// outstanding handlers, and this one is parked on blocked. Releasing it
	// first is what keeps the test from deadlocking on its own fixture.
	defer close(blocked)

	reg := NewRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := reg.Connect(ctx, "clingy", ServerEntry{URL: srv.URL},
		Implementation{Name: "test", Version: "1"}, nil, nil); err != nil {
		t.Fatal(err)
	}

	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		reg.Close()
	}()

	// Close is now stuck on the DELETE. Readers must still be served — before,
	// they queued behind r.mu for as long as the teardown took.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		_ = reg.Get("clingy")
		_ = reg.Connections()
		_ = reg.Len()
	}()
	select {
	case <-readDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Registry readers blocked behind a stalled Close")
	}

	// The DELETE's own timeout eventually frees Close.
	select {
	case <-closeDone:
	case <-time.After(sessionDeleteTimeout + 5*time.Second):
		t.Error("Close never returned")
	}
}
