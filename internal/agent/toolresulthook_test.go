package agent

import (
	"context"
	"strings"
	"testing"
)

func TestToolResultHook_AppendsToMatchingResult(t *testing.T) {
	send := &fakeToolSender{
		replies: []Reply{
			{
				StopReason: "tool_use",
				Blocks: []ContentBlock{
					NewToolUseBlock("call-1", "terminal", map[string]any{"command": "gh pr merge 7"}),
				},
			},
			{Content: "done", StopReason: "end_turn"},
		},
	}
	exec := &fakeExecutor{results: map[string]string{"terminal": "merged"}}
	a := New(send, "m")
	a.ToolResultHook = func(name string, input map[string]any) string {
		if name != "terminal" {
			t.Errorf("hook saw tool %q, want terminal", name)
		}
		if cmd, _ := input["command"].(string); cmd != "gh pr merge 7" {
			t.Errorf("hook saw command %q", cmd)
		}
		return "<nudge>"
	}

	if _, err := a.Run(context.Background(), "merge it", []ToolDefinition{{Name: "terminal"}}, exec); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The model's second call must carry the decorated result.
	var got string
	for _, m := range send.gotMsgs {
		for _, b := range m.Blocks {
			if b.Type == "tool_result" && b.ToolUseID == "call-1" {
				got = b.Result
			}
		}
	}
	if want := "merged\n\n<nudge>"; got != want {
		t.Errorf("tool_result text = %q, want %q", got, want)
	}
}

func TestToolResultHook_EmptyReturnLeavesResultUntouched(t *testing.T) {
	send := &fakeToolSender{
		replies: []Reply{
			{
				StopReason: "tool_use",
				Blocks: []ContentBlock{
					NewToolUseBlock("call-1", "terminal", map[string]any{"command": "ls"}),
				},
			},
			{Content: "done", StopReason: "end_turn"},
		},
	}
	exec := &fakeExecutor{results: map[string]string{"terminal": "files"}}
	a := New(send, "m")
	a.ToolResultHook = func(string, map[string]any) string { return "" }

	if _, err := a.Run(context.Background(), "list", []ToolDefinition{{Name: "terminal"}}, exec); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, m := range send.gotMsgs {
		for _, b := range m.Blocks {
			if b.Type == "tool_result" && b.Result != "files" {
				t.Errorf("tool_result text = %q, want untouched %q", b.Result, "files")
			}
		}
	}
}

func TestApplyToolResultHook_SkipsErroredAndUnmatched(t *testing.T) {
	uses := []ContentBlock{
		NewToolUseBlock("ok", "terminal", map[string]any{"command": "gh pr create"}),
		NewToolUseBlock("bad", "terminal", map[string]any{"command": "gh pr merge"}),
	}
	results := []ContentBlock{
		NewToolResultBlock("ok", "created", false),
		NewToolResultBlock("bad", "denied", true),
	}
	var seen []string
	applyToolResultHook(func(name string, input map[string]any) string {
		cmd, _ := input["command"].(string)
		seen = append(seen, cmd)
		return "N"
	}, uses, results)

	if len(seen) != 1 || seen[0] != "gh pr create" {
		t.Errorf("hook calls = %v, want only the successful call", seen)
	}
	if !strings.HasSuffix(results[0].Result, "\n\nN") {
		t.Errorf("successful result not decorated: %q", results[0].Result)
	}
	if results[1].Result != "denied" {
		t.Errorf("errored result must stay untouched: %q", results[1].Result)
	}
}
