package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// progressServer answers initialize, then serves each tools/call by sending
// reports progress notifications for the call's token, spaced by gap, before
// replying. tokens records the _meta.progressToken of every tools/call.
type progressServer struct {
	tx      *mockTransport
	reports int
	gap     time.Duration
	tokens  chan string
	// foreignToken reports against a token the client never sent.
	foreignToken bool

	stop chan struct{}
	done chan struct{}
}

// send delivers a frame unless the test is tearing down. It must stop before
// the client's Close closes tx.out, or the send would panic.
func (s *progressServer) send(m *Message) bool {
	select {
	case s.tx.out <- m:
		return true
	case <-s.stop:
		return false
	}
}

func (s *progressServer) run(t *testing.T) {
	defer close(s.done)
	for {
		var req *Message
		select {
		case req = <-s.tx.in:
		case <-s.stop:
			return
		}
		if req.IsNotification() {
			continue
		}
		switch req.Method {
		case MethodInitialize:
			b, _ := json.Marshal(InitializeResult{ProtocolVersion: ProtocolVersion, ServerInfo: Implementation{Name: "p"}})
			if !s.send(&Message{JSONRPC: "2.0", ID: req.ID, Result: b}) {
				return
			}
		case MethodToolsCall:
			var p CallToolParams
			if err := json.Unmarshal(req.Params, &p); err != nil {
				t.Errorf("decode tools/call params: %v", err)
				return
			}
			token := ""
			if p.Meta != nil {
				token = p.Meta.ProgressToken
			}
			s.tokens <- token
			tok, _ := json.Marshal(token)
			if s.foreignToken {
				tok = json.RawMessage(`999999`)
			}
			total := float64(s.reports)
			for i := 1; i <= s.reports; i++ {
				select {
				case <-time.After(s.gap):
				case <-s.stop:
					return
				}
				b, _ := json.Marshal(ProgressParams{ProgressToken: tok, Progress: float64(i), Total: &total, Message: "step"})
				if !s.send(&Message{JSONRPC: "2.0", Method: MethodProgress, Params: b}) {
					return
				}
			}
			b, _ := json.Marshal(CallToolResult{Content: []Content{{Type: "text", Text: "done"}}})
			if !s.send(&Message{JSONRPC: "2.0", ID: req.ID, Result: b}) {
				return
			}
		}
	}
}

func startProgressServer(t *testing.T, s *progressServer) *Client {
	t.Helper()
	s.tx = newMockTransport()
	s.tokens = make(chan string, 4)
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	go s.run(t)
	c := NewClient(s.tx, Implementation{Name: "test"})
	t.Cleanup(func() {
		close(s.stop)
		<-s.done
		_ = c.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return c
}

func TestCallToolProgress_DeliversReportsInOrder(t *testing.T) {
	s := &progressServer{reports: 5}
	c := startProgressServer(t, s)

	var got []float64
	res, err := c.CallToolProgress(context.Background(), "build", nil, func(p ProgressParams) {
		got = append(got, p.Progress)
		if p.Total == nil || *p.Total != 5 || p.Message != "step" {
			t.Errorf("report = %+v", p)
		}
	})
	if err != nil {
		t.Fatalf("CallToolProgress: %v", err)
	}
	if len(res.Content) != 1 || res.Content[0].Text != "done" {
		t.Errorf("result = %+v", res)
	}
	if tok := <-s.tokens; !strings.HasPrefix(tok, "progress-") {
		t.Errorf("progressToken = %q, want one sent", tok)
	}
	// All five arrive before the response, so all five must be handed over
	// before CallToolProgress returns — and in the server's order.
	want := []float64{1, 2, 3, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("got %d reports %v, want %v", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reports out of order: %v", got)
		}
	}
}

func TestCallTool_SendsNoProgressToken(t *testing.T) {
	s := &progressServer{}
	c := startProgressServer(t, s)
	if _, err := c.CallTool(context.Background(), "x", nil); err != nil {
		t.Fatal(err)
	}
	// A server may only report progress on a token we sent; plain CallTool
	// must not invite reports nobody would read.
	if tok := <-s.tokens; tok != "" {
		t.Errorf("plain CallTool sent progressToken %q", tok)
	}
}

func TestCallToolProgress_ReportsExtendTheIdleTimeout(t *testing.T) {
	old := defaultCallTimeout
	defaultCallTimeout = 300 * time.Millisecond
	defer func() { defaultCallTimeout = old }()

	// 8 reports × 60ms ≈ 480ms total: past one idle window, but never silent
	// for anywhere near a whole one.
	s := &progressServer{reports: 8, gap: 60 * time.Millisecond}
	c := startProgressServer(t, s)

	n := 0
	if _, err := c.CallToolProgress(context.Background(), "slow", nil, func(ProgressParams) { n++ }); err != nil {
		t.Fatalf("a call that keeps reporting must not time out: %v", err)
	}
	if n != 8 {
		t.Errorf("got %d reports, want 8", n)
	}
}

func TestCallToolProgress_SilenceTimesOut(t *testing.T) {
	old := defaultCallTimeout
	defaultCallTimeout = 80 * time.Millisecond
	defer func() { defaultCallTimeout = old }()

	// One report after 300ms: the call has been silent for a whole idle
	// window by then.
	s := &progressServer{reports: 1, gap: 300 * time.Millisecond}
	c := startProgressServer(t, s)

	_, err := c.CallToolProgress(context.Background(), "stuck", nil, func(ProgressParams) {})
	if !errors.Is(err, errProgressIdle) {
		t.Fatalf("err = %v, want the idle timeout", err)
	}
}

func TestCallToolProgress_MaxTimeoutCapsReporting(t *testing.T) {
	oldIdle, oldMax := defaultCallTimeout, maxProgressCallTimeout
	defaultCallTimeout = 300 * time.Millisecond
	maxProgressCallTimeout = 400 * time.Millisecond
	defer func() { defaultCallTimeout, maxProgressCallTimeout = oldIdle, oldMax }()

	// Reports every 50ms keep the idle window open indefinitely; only the
	// overall cap can end this call.
	s := &progressServer{reports: 40, gap: 50 * time.Millisecond}
	c := startProgressServer(t, s)

	start := time.Now()
	_, err := c.CallToolProgress(context.Background(), "chatty", nil, func(ProgressParams) {})
	if !errors.Is(err, errProgressCap) {
		t.Fatalf("err = %v, want the overall cap", err)
	}
	if el := time.Since(start); el > 1500*time.Millisecond {
		t.Errorf("returned after %s; the cap is 400ms", el)
	}
}

func TestCallToolProgress_CallerDeadlineIsNotExtended(t *testing.T) {
	// Reports every 50ms would keep an idle window alive; a deadline the
	// caller chose must still end the call.
	s := &progressServer{reports: 40, gap: 50 * time.Millisecond}
	c := startProgressServer(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := c.CallToolProgress(ctx, "chatty", nil, func(ProgressParams) {})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the caller's deadline", err)
	}
}

func TestCallToolProgress_IgnoresUnknownToken(t *testing.T) {
	s := &progressServer{reports: 3, foreignToken: true}
	c := startProgressServer(t, s)

	n := 0
	if _, err := c.CallToolProgress(context.Background(), "x", nil, func(ProgressParams) { n++ }); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("got %d reports for a token we never sent", n)
	}
}

func TestProgressKey(t *testing.T) {
	if got := progressKey(json.RawMessage(`"progress-7"`)); got != "progress-7" {
		t.Errorf("string token -> %q", got)
	}
	// An integer is a valid token in general, just never one of ours.
	if got := progressKey(json.RawMessage(`7`)); got != "" {
		t.Errorf("integer token -> %q, want no key", got)
	}
}
