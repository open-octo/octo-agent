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

// A <system-reminder> span appended by the hook (memory save-nudge) is
// model-facing: it must stay on the persisted block but never reach the UI
// event stream, where it would render inside the tool card.
func TestToolResultHook_ReminderStrippedFromEventOutput(t *testing.T) {
	uses := []ContentBlock{
		NewToolUseBlock("call-1", "terminal", map[string]any{"command": "gh pr create"}),
	}
	results := []ContentBlock{NewToolResultBlock("call-1", "created PR #7", false)}
	applyToolResultHook(func(string, map[string]any) string {
		return "<system-reminder>\nsave a memory\n</system-reminder>"
	}, uses, results)

	if !strings.Contains(results[0].Result, "<system-reminder>") {
		t.Fatalf("block must keep the reminder for the model: %q", results[0].Result)
	}

	var events []AgentEvent
	emitToolResultEvents(func(ev AgentEvent) { events = append(events, ev) }, uses, results)
	if len(events) != 1 || events[0].Kind != EventToolDone {
		t.Fatalf("events = %+v, want one EventToolDone", events)
	}
	if strings.Contains(events[0].Output, "system-reminder") || strings.Contains(events[0].Output, "save a memory") {
		t.Errorf("event Output leaks the reminder: %q", events[0].Output)
	}
	if want := "created PR #7"; events[0].Output != want {
		t.Errorf("event Output = %q, want %q (no trailing blank lines)", events[0].Output, want)
	}
}

func TestStripRemindersForDisplay(t *testing.T) {
	clean := "plain output\nwith trailing newline\n"
	if got := StripRemindersForDisplay(clean); got != clean {
		t.Errorf("clean text must be byte-identical, got %q", got)
	}
	decorated := "merged\n\n<system-reminder>\nnudge\n</system-reminder>"
	if got := StripRemindersForDisplay(decorated); got != "merged" {
		t.Errorf("decorated = %q, want %q", got, "merged")
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
