package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// pagingServer answers initialize, then serves tools/list from a fixed list
// of pages, recording the cursor each request carried so a test can assert
// the client actually walked the chain. cursorFor decides what NextCursor a
// page reports, which is how the pathological cases (a repeated cursor, a
// cursor that never ends) are simulated.
type pagingServer struct {
	tx        *mockTransport
	pages     [][]Tool
	cursorFor func(page int, lastCursor string) string

	mu   sync.Mutex
	seen []string // cursor received per tools/list request, in order

	stopped  chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func newPagingServer(pages [][]Tool) *pagingServer {
	return &pagingServer{
		tx:      newMockTransport(),
		pages:   pages,
		stopped: make(chan struct{}),
		// Default: page N reports cursor "cN" until the last page, which ends
		// the walk with an empty cursor.
		cursorFor: func(page int, _ string) string {
			if page >= len(pages)-1 {
				return ""
			}
			return "c" + string(rune('1'+page))
		},
	}
}

func (s *pagingServer) cursors() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

func (s *pagingServer) start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		reqs := 0
		for {
			select {
			case <-s.stopped:
				return
			case req, ok := <-s.tx.in:
				if !ok {
					return
				}
				if req.IsNotification() {
					continue
				}
				switch req.Method {
				case MethodInitialize:
					b, _ := json.Marshal(InitializeResult{
						ProtocolVersion: ProtocolVersion,
						Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
						ServerInfo:      Implementation{Name: "pager", Version: "1"},
					})
					s.tx.out <- &Message{JSONRPC: "2.0", ID: req.ID, Result: b}
				case MethodToolsList:
					var p PaginatedParams
					_ = json.Unmarshal(req.Params, &p)
					s.mu.Lock()
					s.seen = append(s.seen, p.Cursor)
					s.mu.Unlock()

					idx := reqs
					reqs++
					var tools []Tool
					if idx < len(s.pages) {
						tools = s.pages[idx]
					}
					b, _ := json.Marshal(ListToolsResult{
						Tools:      tools,
						NextCursor: s.cursorFor(idx, p.Cursor),
					})
					s.tx.out <- &Message{JSONRPC: "2.0", ID: req.ID, Result: b}
				default:
					s.tx.out <- &Message{
						JSONRPC: "2.0", ID: req.ID,
						Error: &RPCError{Code: -32601, Message: "unexpected " + req.Method},
					}
				}
			}
		}
	}()
}

func (s *pagingServer) stop() {
	s.stopOnce.Do(func() {
		close(s.stopped)
		_ = s.tx.Close()
	})
	s.wg.Wait()
}

func (s *pagingServer) client(t *testing.T) *Client {
	t.Helper()
	s.start()
	c := NewClient(s.tx, Implementation{Name: "test", Version: "1"})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return c
}

// TestListTools_FollowsCursor is the regression this pagination exists for:
// a server that splits its tools across pages used to have everything past
// page 1 silently dropped, with no error to hint at it.
func TestListTools_FollowsCursor(t *testing.T) {
	srv := newPagingServer([][]Tool{
		{{Name: "a"}, {Name: "b"}},
		{{Name: "c"}},
		{{Name: "d"}, {Name: "e"}},
	})
	defer srv.stop()
	c := srv.client(t)
	defer c.Close()

	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tl := range tools {
		got = append(got, tl.Name)
	}
	if strings.Join(got, ",") != "a,b,c,d,e" {
		t.Errorf("tools = %v, want all five across three pages", got)
	}
	// First request carries no cursor; each later one carries what the
	// previous page returned.
	if want := ",c1,c2"; strings.Join(srv.cursors(), ",") != want {
		t.Errorf("cursors sent = %q, want %q", strings.Join(srv.cursors(), ","), want)
	}
}

// TestListTools_SinglePageSendsNoCursor pins the wire compatibility claim:
// an unpaginated server sees exactly one request, with no cursor.
func TestListTools_SinglePageSendsNoCursor(t *testing.T) {
	srv := newPagingServer([][]Tool{{{Name: "only"}}})
	defer srv.stop()
	c := srv.client(t)
	defer c.Close()

	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "only" {
		t.Errorf("tools = %v, want [only]", tools)
	}
	if got := srv.cursors(); len(got) != 1 || got[0] != "" {
		t.Errorf("cursors sent = %q, want exactly one empty cursor", got)
	}
}

// TestListTools_RepeatedCursorStops guards the loop: a server that keeps
// handing back the cursor it was just given must not spin us forever.
func TestListTools_RepeatedCursorStops(t *testing.T) {
	srv := newPagingServer([][]Tool{{{Name: "a"}}, {{Name: "b"}}})
	srv.cursorFor = func(page int, _ string) string {
		if page == 0 {
			return "stuck"
		}
		return "stuck" // same cursor again — the client must give up here
	}
	defer srv.stop()
	c := srv.client(t)
	defer c.Close()

	done := make(chan struct{})
	var tools []Tool
	var err error
	go func() {
		tools, err = c.ListTools(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ListTools never returned — the repeated cursor looped")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Both pages fetched, then the repeat ends it.
	if len(tools) != 2 {
		t.Errorf("tools = %v, want the two pages fetched before the repeat", tools)
	}
}

// TestListTools_RunawayPagingErrors: a server handing back an endless chain
// of fresh cursors gets cut off with an error rather than being followed
// forever or silently truncated.
func TestListTools_RunawayPagingErrors(t *testing.T) {
	srv := newPagingServer([][]Tool{{{Name: "a"}}})
	page := 0
	srv.cursorFor = func(_ int, _ string) string {
		page++
		return "cursor-" + string(rune('a'+page%26)) + string(rune('a'+page/26))
	}
	defer srv.stop()
	c := srv.client(t)
	defer c.Close()

	done := make(chan struct{})
	var err error
	go func() {
		_, err = c.ListTools(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ListTools never returned on a runaway cursor chain")
	}
	if err == nil || !strings.Contains(err.Error(), "did not terminate") {
		t.Errorf("err = %v, want a did-not-terminate error", err)
	}
	if n := len(srv.cursors()); n != maxListPages {
		t.Errorf("requests = %d, want the %d-page cap", n, maxListPages)
	}
}

// TestInitialize_RecordsNegotiatedVersion: the session runs on whatever the
// server answered with, not on the version we asked for.
func TestInitialize_RecordsNegotiatedVersion(t *testing.T) {
	tx := newMockTransport()
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		for {
			select {
			case <-stopped:
				return
			case req, ok := <-tx.in:
				if !ok || req.IsNotification() {
					continue
				}
				if req.Method == MethodInitialize {
					b, _ := json.Marshal(InitializeResult{
						ProtocolVersion: "2025-03-26", // newer than we requested
						Capabilities:    ServerCapabilities{},
						ServerInfo:      Implementation{Name: "newer", Version: "1"},
					})
					tx.out <- &Message{JSONRPC: "2.0", ID: req.ID, Result: b}
				}
			}
		}
	}()

	c := NewClient(tx, Implementation{Name: "test", Version: "1"})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if got := c.ProtocolVersion(); got != "2025-03-26" {
		t.Errorf("ProtocolVersion() = %q, want the server's answer 2025-03-26", got)
	}
}

// versionRecordingTransport captures SetProtocolVersion so we can assert the
// client pushes the negotiated revision down to transports that need it.
type versionRecordingTransport struct {
	*mockTransport
	mu  sync.Mutex
	got string
}

func (v *versionRecordingTransport) SetProtocolVersion(s string) {
	v.mu.Lock()
	v.got = s
	v.mu.Unlock()
}

func (v *versionRecordingTransport) version() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.got
}

func TestInitialize_PushesVersionToTransport(t *testing.T) {
	tx := &versionRecordingTransport{mockTransport: newMockTransport()}
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		for {
			select {
			case <-stopped:
				return
			case req, ok := <-tx.in:
				if !ok || req.IsNotification() {
					continue
				}
				if req.Method == MethodInitialize {
					b, _ := json.Marshal(InitializeResult{
						ProtocolVersion: "2025-06-18",
						Capabilities:    ServerCapabilities{},
						ServerInfo:      Implementation{Name: "s", Version: "1"},
					})
					tx.out <- &Message{JSONRPC: "2.0", ID: req.ID, Result: b}
				}
			}
		}
	}()

	c := NewClient(tx, Implementation{Name: "test", Version: "1"})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if got := tx.version(); got != "2025-06-18" {
		t.Errorf("transport received version %q, want 2025-06-18", got)
	}
}
