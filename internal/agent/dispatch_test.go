package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCanParallelize(t *testing.T) {
	ro := func(name string) toolCall { return toolCall{block: NewToolUseBlock("id", name, nil)} }

	cases := []struct {
		name  string
		calls []toolCall
		want  bool
	}{
		{"two read-only", []toolCall{ro("read_file"), ro("grep")}, true},
		{"three read-only", []toolCall{ro("read_file"), ro("grep"), ro("glob")}, true},
		{"single read-only", []toolCall{ro("read_file")}, false},
		{"mixed read-only + write", []toolCall{ro("read_file"), ro("write_file")}, false},
		{"mixed read-only + terminal", []toolCall{ro("grep"), ro("terminal")}, false},
		{"empty", nil, false},
		{
			"denied call ignored, remaining single → serial",
			[]toolCall{{block: NewToolUseBlock("a", "read_file", nil), denyReason: "no"}, ro("grep")},
			false,
		},
		{
			"denied call ignored, two executable read-only → parallel",
			[]toolCall{{block: NewToolUseBlock("a", "write_file", nil), denyReason: "no"}, ro("grep"), ro("glob")},
			true,
		},
		{
			// sub_agent is concurrency-safe: each runs in an isolated child, and
			// the manager/token accounting are mutex-guarded. A fan-out must run
			// concurrently, else 7 sub-agents block one another.
			"two sub_agent calls → parallel",
			[]toolCall{ro("sub_agent"), ro("sub_agent")},
			true,
		},
		{
			"sub_agent + read_file → parallel",
			[]toolCall{ro("sub_agent"), ro("read_file")},
			true,
		},
		{
			// A mutating tool in the batch still forces serial, even alongside
			// sub_agent — write_file is not concurrency-safe.
			"sub_agent + write_file → serial",
			[]toolCall{ro("sub_agent"), ro("write_file")},
			false,
		},
	}
	for _, tc := range cases {
		if got := canParallelize(tc.calls); got != tc.want {
			t.Errorf("%s: canParallelize = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// barrierExec proves concurrency: every Execute marks arrival then waits for
// ALL expected calls to arrive. If the dispatcher runs them serially the first
// call blocks forever (the others never start) and the test's timeout fires.
type barrierExec struct {
	wg    sync.WaitGroup
	mu    sync.Mutex
	order []string
}

func (b *barrierExec) Execute(_ context.Context, name string, _ map[string]any) (ToolResult, error) {
	b.mu.Lock()
	b.order = append(b.order, name)
	b.mu.Unlock()
	b.wg.Done() // arrived
	b.wg.Wait() // release only once everyone has arrived → requires concurrency
	return ToolResult{Text: "ok:" + name}, nil
}

func TestDispatchTools_ParallelReadOnly(t *testing.T) {
	exec := &barrierExec{}
	exec.wg.Add(3)
	blocks := []ContentBlock{
		NewToolUseBlock("c1", "read_file", map[string]any{"path": "a"}),
		NewToolUseBlock("c2", "grep", map[string]any{"pattern": "x"}),
		NewToolUseBlock("c3", "glob", map[string]any{"pattern": "*"}),
	}

	done := make(chan []ContentBlock, 1)
	go func() {
		r, _ := dispatchTools(context.Background(), exec, blocks, nil, nil)
		done <- r
	}()

	select {
	case results := <-done:
		// Results must be in block order even though execution was concurrent.
		want := []string{"c1", "c2", "c3"}
		if len(results) != 3 {
			t.Fatalf("got %d results, want 3", len(results))
		}
		for i, w := range want {
			if results[i].ToolUseID != w {
				t.Errorf("results[%d].ToolUseID = %q, want %q", i, results[i].ToolUseID, w)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("read-only batch did not run concurrently — the arrival barrier never released (serial dispatch)")
	}
}

func TestDispatchTools_ParallelSubAgents(t *testing.T) {
	// A fan-out of sub_agent calls must run concurrently, not serially — the
	// whole point of dispatching several at once. The arrival barrier only
	// releases if all three start together.
	exec := &barrierExec{}
	exec.wg.Add(3)
	blocks := []ContentBlock{
		NewToolUseBlock("s1", "sub_agent", map[string]any{"prompt": "a"}),
		NewToolUseBlock("s2", "sub_agent", map[string]any{"prompt": "b"}),
		NewToolUseBlock("s3", "sub_agent", map[string]any{"prompt": "c"}),
	}

	done := make(chan []ContentBlock, 1)
	go func() {
		r, _ := dispatchTools(context.Background(), exec, blocks, nil, nil)
		done <- r
	}()

	select {
	case results := <-done:
		want := []string{"s1", "s2", "s3"}
		if len(results) != 3 {
			t.Fatalf("got %d results, want 3", len(results))
		}
		for i, w := range want {
			if results[i].ToolUseID != w {
				t.Errorf("results[%d].ToolUseID = %q, want %q", i, results[i].ToolUseID, w)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sub_agent batch did not run concurrently — the arrival barrier never released (serial dispatch)")
	}
}

// countingExec is a concurrency-safe executor for the serial-path test.
type countingExec struct {
	mu     sync.Mutex
	called []string
}

func (c *countingExec) Execute(_ context.Context, name string, _ map[string]any) (ToolResult, error) {
	c.mu.Lock()
	c.called = append(c.called, name)
	c.mu.Unlock()
	return ToolResult{Text: "ok:" + name}, nil
}

func TestDispatchTools_MixedBatchSerialCorrect(t *testing.T) {
	// A write_file in the batch forces the serial path; results must still be
	// correct and in order.
	exec := &countingExec{}
	blocks := []ContentBlock{
		NewToolUseBlock("c1", "read_file", map[string]any{"path": "a"}),
		NewToolUseBlock("c2", "write_file", map[string]any{"path": "b", "content": "x"}),
		NewToolUseBlock("c3", "grep", map[string]any{"pattern": "y"}),
	}
	results, err := dispatchTools(context.Background(), exec, blocks, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, w := range []string{"c1", "c2", "c3"} {
		if results[i].ToolUseID != w {
			t.Errorf("results[%d] = %q, want %q", i, results[i].ToolUseID, w)
		}
		if results[i].Result != "ok:"+blocks[i].Name {
			t.Errorf("results[%d].Result = %q", i, results[i].Result)
		}
	}
}

// denyOneGate denies a specific tool name and allows everything else.
type denyOneGate struct{ deny string }

func (g denyOneGate) Check(_ context.Context, name string, _ map[string]any) (bool, string) {
	if name == g.deny {
		return false, "permission_denied: " + name
	}
	return true, ""
}

func TestDispatchTools_DeniedCallInParallelBatch(t *testing.T) {
	// All read-only (parallel-eligible), but the gate denies the grep. The
	// denied call yields an IsError result; the others still execute.
	exec := &countingExec{}
	gate := denyOneGate{deny: "grep"}
	blocks := []ContentBlock{
		NewToolUseBlock("c1", "read_file", map[string]any{"path": "a"}),
		NewToolUseBlock("c2", "grep", map[string]any{"pattern": "x"}),
		NewToolUseBlock("c3", "glob", map[string]any{"pattern": "*"}),
	}
	results, err := dispatchTools(context.Background(), exec, blocks, nil, gate)
	if err != nil {
		t.Fatal(err)
	}
	if !results[1].IsError || !strings.Contains(results[1].Result, "permission_denied") {
		t.Errorf("grep result should be a denial error, got %+v", results[1])
	}
	if results[0].IsError || results[2].IsError {
		t.Errorf("allowed calls should have executed: %+v / %+v", results[0], results[2])
	}
	// Executor saw only the two allowed calls.
	if len(exec.called) != 2 {
		t.Errorf("executor calls = %d, want 2 (grep was gated out)", len(exec.called))
	}
}

// recordingHandler collects AgentEvents; concurrency-safe because dispatchTools'
// parallel path may emit result events from several goroutines.
type recordingHandler struct {
	mu     sync.Mutex
	events []AgentEvent
}

func (h *recordingHandler) handle(ev AgentEvent) {
	h.mu.Lock()
	h.events = append(h.events, ev)
	h.mu.Unlock()
}

func (h *recordingHandler) resultKinds() map[string]EventKind {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]EventKind{}
	for _, ev := range h.events {
		if ev.Kind == EventToolDone || ev.Kind == EventToolError {
			out[ev.ToolID] = ev.Kind
		}
	}
	return out
}

// dispatchTools must emit a result event for EVERY tool as it finishes — not
// batched by the caller after the whole set completes. This is the regression
// guard for a parallel sub_agent fan-out whose cards stayed "running" (and were
// lost on a mid-batch refresh) until the slowest child returned. Covers the
// parallel path (done) and a gate-denied call (error) in the same batch.
func TestDispatchTools_EmitsResultEventPerTool(t *testing.T) {
	h := &recordingHandler{}
	exec := &countingExec{}
	gate := denyOneGate{deny: "grep"}
	blocks := []ContentBlock{
		NewToolUseBlock("c1", "read_file", map[string]any{"path": "a"}),
		NewToolUseBlock("c2", "grep", map[string]any{"pattern": "x"}),
		NewToolUseBlock("c3", "glob", map[string]any{"pattern": "*"}),
	}
	if _, err := dispatchTools(context.Background(), exec, blocks, h.handle, gate); err != nil {
		t.Fatal(err)
	}
	kinds := h.resultKinds()
	if len(kinds) != 3 {
		t.Fatalf("result events for %d tools, want 3: %v", len(kinds), kinds)
	}
	if kinds["c1"] != EventToolDone || kinds["c3"] != EventToolDone {
		t.Errorf("allowed tools should emit EventToolDone: %v", kinds)
	}
	if kinds["c2"] != EventToolError {
		t.Errorf("gate-denied tool should emit EventToolError: %v", kinds)
	}
}

// The serial path emits per-tool too (single-tool batch → not parallelized).
func TestDispatchTools_EmitsResultEventSerial(t *testing.T) {
	h := &recordingHandler{}
	exec := &countingExec{}
	blocks := []ContentBlock{NewToolUseBlock("only", "read_file", map[string]any{"path": "a"})}
	if _, err := dispatchTools(context.Background(), exec, blocks, h.handle, nil); err != nil {
		t.Fatal(err)
	}
	if k := h.resultKinds(); len(k) != 1 || k["only"] != EventToolDone {
		t.Fatalf("serial result events = %v, want {only: done}", k)
	}
}
