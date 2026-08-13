package agent

import (
	"context"
	"strings"
	"testing"
)

// panicExecutor panics instead of running a tool.
type panicExecutor struct{}

func (panicExecutor) Execute(context.Context, string, map[string]any) (ToolResult, error) {
	panic("tool exploded")
}

// TestDispatchTools_ParallelPanicBecomesToolResult covers the parallel branch,
// which is the only one that runs a tool off the turn's own goroutine — the
// caller's recover can't reach it, and the desktop build hosts the server
// in-process, so a panicking tool used to close the app.
//
// Filling the slot matters as much as surviving: a tool_use with no matching
// tool_result makes the *next* request malformed, so the failure would come
// back from the provider one turn later, far from its cause.
func TestDispatchTools_ParallelPanicBecomesToolResult(t *testing.T) {
	blocks := []ContentBlock{
		{Type: "tool_use", ID: "t1", Name: "read_file"},
		{Type: "tool_use", ID: "t2", Name: "read_file"},
	}

	out, err := dispatchTools(context.Background(), panicExecutor{}, blocks, nil, nil)
	if err != nil {
		t.Fatalf("dispatchTools: %v", err)
	}
	if len(out) != len(blocks) {
		t.Fatalf("got %d result blocks for %d calls: %+v", len(out), len(blocks), out)
	}
	for i, b := range out {
		if b.Type != "tool_result" {
			t.Errorf("block %d is %q, want tool_result", i, b.Type)
		}
		if !b.IsError {
			t.Errorf("block %d should be an error result", i)
		}
		if !strings.Contains(b.Result, "panicked") {
			t.Errorf("block %d should report the panic, got %q", i, b.Result)
		}
	}
	if out[0].ToolUseID != "t1" || out[1].ToolUseID != "t2" {
		t.Errorf("results lost their tool_use ids / order: %q, %q", out[0].ToolUseID, out[1].ToolUseID)
	}
}
