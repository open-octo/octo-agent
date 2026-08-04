package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// listChangedServer serves tools/list from a swappable set and can push a
// tools/list_changed notification down an SSE response, which is how a real
// server announces a new set mid-session.
type listChangedServer struct {
	mu        sync.Mutex
	tools     []string
	listCalls int
	// pushOnNextCall makes the next tools/call response an SSE stream that
	// carries a list_changed notification ahead of the actual result.
	pushOnNextCall bool
}

func (s *listChangedServer) setTools(names ...string) {
	s.mu.Lock()
	s.tools = names
	s.mu.Unlock()
}

func (s *listChangedServer) armPush() {
	s.mu.Lock()
	s.pushOnNextCall = true
	s.mu.Unlock()
}

func (s *listChangedServer) lists() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listCalls
}

func (s *listChangedServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
				Capabilities: ServerCapabilities{
					Tools:     &ToolsCapability{ListChanged: true},
					Resources: &ResourcesCapability{},
					Prompts:   &PromptsCapability{},
				},
				ServerInfo: Implementation{Name: "churn", Version: "1"},
			})
		case MethodToolsList:
			s.mu.Lock()
			s.listCalls++
			names := append([]string(nil), s.tools...)
			s.mu.Unlock()
			out := make([]Tool, 0, len(names))
			for _, n := range names {
				out = append(out, Tool{Name: n})
			}
			writeJSONRPC(w, in.ID, ListToolsResult{Tools: out})
		case MethodResourcesList:
			writeJSONRPC(w, in.ID, ListResourcesResult{})
		case MethodPromptsList:
			writeJSONRPC(w, in.ID, ListPromptsResult{})
		case MethodToolsCall:
			s.mu.Lock()
			push := s.pushOnNextCall
			s.pushOnNextCall = false
			s.mu.Unlock()
			if !push {
				writeJSONRPC(w, in.ID, CallToolResult{Content: []Content{{Type: "text", Text: "ok"}}})
				return
			}
			// SSE response: notification first, then the result.
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			notif, _ := json.Marshal(Message{JSONRPC: "2.0", Method: MethodToolListChanged})
			fmt.Fprintf(w, "data: %s\n\n", notif)
			res, _ := json.Marshal(CallToolResult{Content: []Content{{Type: "text", Text: "ok"}}})
			resp, _ := json.Marshal(Message{JSONRPC: "2.0", ID: in.ID, Result: res})
			fmt.Fprintf(w, "data: %s\n\n", resp)
		default:
			writeJSONRPC(w, in.ID, map[string]any{})
		}
	}
}

func connectTo(t *testing.T, url string) (*Registry, *Connection) {
	t.Helper()
	reg := NewRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := reg.Connect(ctx, "churn", ServerEntry{URL: url},
		Implementation{Name: "test", Version: "1"}, nil, nil); err != nil {
		t.Fatalf("connect: %v", err)
	}
	conn := reg.Get("churn")
	if conn == nil {
		t.Fatal("no connection registered")
	}
	return reg, conn
}

// waitForTools polls until the connection's tool list matches want, which is
// the observable effect of the refresh. The refresh is asynchronous by design
// (it can't run on the receive loop without deadlocking), so a poll is the
// honest way to await it.
func waitForTools(t *testing.T, conn *Connection, want string, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		var got string
		for i, tl := range conn.Tools() {
			if i > 0 {
				got += ","
			}
			got += tl.Name
		}
		if got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	var got []string
	for _, tl := range conn.Tools() {
		got = append(got, tl.Name)
	}
	t.Fatalf("tools = %v, never became %q within %v", got, want, limit)
}

// TestListChanged_RefreshesTools is the behaviour the feature exists for: the
// server says its tools changed, and the connection reflects the new set
// without anyone reconnecting it by hand.
func TestListChanged_RefreshesTools(t *testing.T) {
	fake := &listChangedServer{}
	fake.setTools("old")
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	reg, conn := connectTo(t, srv.URL)
	defer reg.Close()

	waitForTools(t, conn, "old", time.Second)

	// The server swaps its tool set and announces it on the next call's stream.
	fake.setTools("fresh", "alsofresh")
	fake.armPush()
	if _, err := conn.Client.CallTool(context.Background(), "old", nil); err != nil {
		t.Fatal(err)
	}
	waitForTools(t, conn, "fresh,alsofresh", 5*time.Second)
}

// TestListChanged_FailedRefreshKeepsPreviousList: if the re-list fails, the
// old listing must stay. An empty list would make the server look like it has
// no tools at all, which is worse than a stale one the model can still call.
func TestListChanged_FailedRefreshKeepsPreviousList(t *testing.T) {
	var failLists atomic.Bool
	var mu sync.Mutex
	tools := []string{"keepme"}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				Capabilities:    ServerCapabilities{Tools: &ToolsCapability{ListChanged: true}},
				ServerInfo:      Implementation{Name: "flaky", Version: "1"},
			})
		case MethodToolsList:
			if failLists.Load() {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			mu.Lock()
			names := append([]string(nil), tools...)
			mu.Unlock()
			out := make([]Tool, 0, len(names))
			for _, n := range names {
				out = append(out, Tool{Name: n})
			}
			writeJSONRPC(w, in.ID, ListToolsResult{Tools: out})
		case MethodToolsCall:
			// Announce the change, then fail every subsequent list.
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			failLists.Store(true)
			notif, _ := json.Marshal(Message{JSONRPC: "2.0", Method: MethodToolListChanged})
			fmt.Fprintf(w, "data: %s\n\n", notif)
			res, _ := json.Marshal(CallToolResult{Content: []Content{{Type: "text", Text: "ok"}}})
			resp, _ := json.Marshal(Message{JSONRPC: "2.0", ID: in.ID, Result: res})
			fmt.Fprintf(w, "data: %s\n\n", resp)
		default:
			writeJSONRPC(w, in.ID, map[string]any{})
		}
	}))
	defer srv.Close()

	reg, conn := connectTo(t, srv.URL)
	defer reg.Close()
	waitForTools(t, conn, "keepme", time.Second)

	if _, err := conn.Client.CallTool(context.Background(), "keepme", nil); err != nil {
		t.Fatal(err)
	}
	// Give the (failing) refresh time to run and not clobber anything.
	time.Sleep(300 * time.Millisecond)
	got := conn.Tools()
	if len(got) != 1 || got[0].Name != "keepme" {
		t.Errorf("tools = %v, want the previous listing kept after a failed refresh", got)
	}
}

// TestListChanged_BurstCoalesces: a server firing many notifications must not
// produce one list call each. The contract is "at most one refresh in flight
// plus one queued", so a big burst collapses into a small number of passes.
func TestListChanged_BurstCoalesces(t *testing.T) {
	fake := &listChangedServer{}
	fake.setTools("t1")
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	reg, conn := connectTo(t, srv.URL)
	defer reg.Close()
	waitForTools(t, conn, "t1", time.Second)

	before := fake.lists()
	const burst = 40
	var wg sync.WaitGroup
	for range burst {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn.refreshSurfaces(MethodToolListChanged)
		}()
	}
	wg.Wait()
	// Let any queued follow-up pass finish.
	time.Sleep(300 * time.Millisecond)

	// Each coalesced pass costs at most 3 list calls (tools+resources+prompts
	// on a follow-up). 40 notifications collapsing to a handful of passes is
	// the point; one-per-notification would be ~40.
	if got := fake.lists() - before; got > burst/2 {
		t.Errorf("tools/list calls = %d for %d notifications; coalescing is not working", got, burst)
	}
}

// TestListChanged_ConcurrentReadersSeeNoRace: a refresh replaces the listings
// while other goroutines read them — a data race without listMu. Meaningful
// under -race.
func TestListChanged_ConcurrentReadersSeeNoRace(t *testing.T) {
	fake := &listChangedServer{}
	fake.setTools("a", "b")
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	reg, conn := connectTo(t, srv.URL)
	defer reg.Close()

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
					for _, tl := range conn.Tools() {
						_ = tl.Name
					}
					_ = conn.Resources()
					_ = conn.Prompts()
				}
			}
		}()
	}

	for i := range 10 {
		fake.setTools(fmt.Sprintf("gen%d", i), fmt.Sprintf("gen%d-b", i))
		conn.refreshSurfaces(MethodToolListChanged)
	}
	close(stop)
	wg.Wait()
}

// TestListChanged_UnrelatedNotificationIgnored: only the list_changed methods
// trigger work. A progress or log notification must not cause list traffic.
func TestListChanged_UnrelatedNotificationIgnored(t *testing.T) {
	fake := &listChangedServer{}
	fake.setTools("t1")
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	reg, conn := connectTo(t, srv.URL)
	defer reg.Close()
	waitForTools(t, conn, "t1", time.Second)

	before := fake.lists()
	// Drive the client's handler directly with methods it should ignore.
	conn.Client.notifyMu.Lock()
	fn := conn.Client.onNotification
	conn.Client.notifyMu.Unlock()
	if fn == nil {
		t.Fatal("no notification handler installed")
	}
	fn("notifications/progress")
	fn("notifications/message")
	time.Sleep(200 * time.Millisecond)

	if got := fake.lists() - before; got != 0 {
		t.Errorf("unrelated notifications caused %d list calls, want 0", got)
	}
}
