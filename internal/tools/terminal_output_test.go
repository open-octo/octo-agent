package tools

import (
	"context"
	"strings"
	"testing"
)

// TestTerminalOutputTool_LinesSnapshot verifies the lines param trims to the
// last N lines and that the snapshot is non-advancing (a second call returns
// the same tail, not "new since last read").
func TestTerminalOutputTool_LinesSnapshot(t *testing.T) {
	m := NewBackgroundManager()
	outTool := TerminalOutputTool{mgr: m}
	ctx := context.Background()

	// Print 5 lines then exit; wait for completion.
	if _, err := (TerminalTool{mgr: m}).Execute(ctx, "terminal", map[string]any{
		"command":           "printf 'l1\\nl2\\nl3\\nl4\\nl5\\n'",
		"run_in_background": "interactive",
	}); err != nil {
		t.Fatalf("launch: %v", err)
	}
	waitFor(t, "exit", func() bool {
		_, s, _, _, _ := m.Read("bg_1")
		return strings.HasPrefix(s, "exited")
	})

	// Last 2 lines only.
	res, err := outTool.Execute(ctx, "terminal_output", map[string]any{"id": "bg_1", "lines": float64(2)})
	if err != nil {
		t.Fatalf("terminal_output: %v", err)
	}
	if !strings.Contains(res.Text, "l4") || !strings.Contains(res.Text, "l5") {
		t.Errorf("tail(2) should contain the last two lines, got %q", res.Text)
	}
	if strings.Contains(res.Text, "l1") {
		t.Errorf("tail(2) should not contain the first line, got %q", res.Text)
	}

	// Idempotent: a second identical call returns the same snapshot.
	res2, _ := outTool.Execute(ctx, "terminal_output", map[string]any{"id": "bg_1", "lines": float64(2)})
	if res2.Text != res.Text {
		t.Errorf("snapshot should be idempotent:\nfirst:  %q\nsecond: %q", res.Text, res2.Text)
	}
}
