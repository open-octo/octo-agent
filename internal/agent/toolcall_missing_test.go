package agent

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

// captureToolCallSlog swaps the slog default for a buffer-backed handler and
// restores the real one on cleanup. Capturing slog.Default() inside the
// cleanup instead would restore the buffer handler, silencing later tests.
func captureToolCallSlog(t *testing.T) *strings.Builder {
	t.Helper()
	var buf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// A provider that answers "tool_use" without a tool_use block used to send
// runLoop around again: an empty batch dispatches to nothing, the duplicate
// detector skips empty batches, and the turn ran to its iteration cap. The
// sender here holds exactly one reply and errors on a second call, so a
// regression shows up as that error rather than as a slow test.
func TestRunLoop_ToolUseStopWithNoToolCall_EndsTurn(t *testing.T) {
	send := &fakeToolSender{
		replies: []Reply{{Model: "some-model", StopReason: "tool_use"}},
	}
	a := New(send, "m")
	exec := &fakeExecutor{}

	reply, err := a.Run(context.Background(), "go", []ToolDefinition{{Name: "bash"}}, exec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if send.calls != 1 {
		t.Errorf("provider called %d times, want 1 — the loop went around again", send.calls)
	}
	if reply.Content != "" {
		t.Errorf("Content = %q, want empty", reply.Content)
	}
}

// The reply's text is the only thing the model actually produced, so ending
// the turn must not throw it away.
func TestRunLoop_ToolUseStopWithNoToolCall_KeepsText(t *testing.T) {
	send := &fakeToolSender{
		replies: []Reply{{
			Model:      "some-model",
			StopReason: "tool_use",
			Blocks:     []ContentBlock{{Type: "text", Text: "here is the answer"}},
		}},
	}
	a := New(send, "m")

	reply, err := a.Run(context.Background(), "go", []ToolDefinition{{Name: "bash"}}, &fakeExecutor{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if send.calls != 1 {
		t.Errorf("provider called %d times, want 1", send.calls)
	}
	// runLoop rebuilds Content from the text blocks when the reply carried
	// none of its own.
	if reply.Content != "here is the answer" {
		t.Errorf("Content = %q, want the text block preserved", reply.Content)
	}
}

// History must not gain a message with neither content nor blocks: that is
// what the next request would be rejected for.
func TestRunLoop_ToolUseStopWithNoToolCall_LeavesNoEmptyMessage(t *testing.T) {
	send := &fakeToolSender{
		replies: []Reply{{Model: "some-model", StopReason: "tool_use"}},
	}
	a := New(send, "m")

	if _, err := a.Run(context.Background(), "go", []ToolDefinition{{Name: "bash"}}, &fakeExecutor{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for i, m := range a.History.Snapshot() {
		if m.Content == "" && len(m.Blocks) == 0 {
			t.Errorf("messages[%d] (%s) has neither content nor blocks", i, m.Role)
		}
	}
}

func TestRunLoop_ToolUseStopWithNoToolCall_Warns(t *testing.T) {
	logs := captureToolCallSlog(t)

	send := &fakeToolSender{
		replies: []Reply{{Model: "some-model", StopReason: "tool_use"}},
	}
	a := New(send, "m")

	if _, err := a.Run(context.Background(), "go", []ToolDefinition{{Name: "bash"}}, &fakeExecutor{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := logs.String()
	for _, want := range []string{
		"stop reason is tool_use but the reply has no tool call",
		"model=some-model",
		"has_text=false",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q missing %q", out, want)
		}
	}
}

// The ordinary path must be untouched: a real tool_use round still dispatches
// and still runs the second provider call that produces the answer.
func TestRunLoop_RealToolUseRoundStillDispatches(t *testing.T) {
	send := &fakeToolSender{
		replies: []Reply{
			{
				StopReason: "tool_use",
				Blocks: []ContentBlock{
					NewToolUseBlock("call-1", "bash", map[string]any{"command": "echo hi"}),
				},
			},
			{Content: "the output was: hi", StopReason: "end_turn"},
		},
	}
	exec := &fakeExecutor{results: map[string]string{"bash": "hi"}}
	a := New(send, "m")

	reply, err := a.Run(context.Background(), "run echo hi", []ToolDefinition{{Name: "bash"}}, exec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if send.calls != 2 {
		t.Errorf("provider called %d times, want 2", send.calls)
	}
	if reply.Content != "the output was: hi" {
		t.Errorf("Content = %q", reply.Content)
	}
}

func TestHasToolUseBlock(t *testing.T) {
	cases := []struct {
		name   string
		blocks []ContentBlock
		want   bool
	}{
		{"nil", nil, false},
		{"empty", []ContentBlock{}, false},
		{"text only", []ContentBlock{{Type: "text", Text: "hi"}}, false},
		{"thinking only", []ContentBlock{{Type: "thinking", Thinking: "hmm"}}, false},
		{"tool_use", []ContentBlock{{Type: "tool_use", ID: "t1", Name: "bash"}}, true},
		{"text then tool_use", []ContentBlock{
			{Type: "text", Text: "on it"},
			{Type: "tool_use", ID: "t1", Name: "bash"},
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasToolUseBlock(c.blocks); got != c.want {
				t.Errorf("hasToolUseBlock() = %v, want %v", got, c.want)
			}
		})
	}
}
