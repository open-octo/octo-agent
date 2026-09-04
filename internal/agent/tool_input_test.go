package agent

import (
	"context"
	"strings"
	"testing"
)

// A provider hands over the raw argument JSON; the block must carry either the
// parsed map or a usable description of why it could not be parsed — never a
// nil map that a tool would read as "no arguments".
func TestNewToolUseBlockFromJSON(t *testing.T) {
	t.Run("valid object", func(t *testing.T) {
		b := NewToolUseBlockFromJSON("c1", "edit_file", `{"path":"a.go","old_string":"x","new_string":"y"}`)
		if b.InputError != "" {
			t.Fatalf("unexpected InputError %q", b.InputError)
		}
		if b.Input["path"] != "a.go" || b.Input["new_string"] != "y" {
			t.Errorf("Input = %v", b.Input)
		}
	})

	t.Run("empty and null give an empty non-nil map", func(t *testing.T) {
		for _, raw := range []string{"", "   ", "null"} {
			b := NewToolUseBlockFromJSON("c1", "t", raw)
			if b.InputError != "" || b.Input == nil || len(b.Input) != 0 {
				t.Errorf("raw %q: Input=%v (nil=%v) InputError=%q", raw, b.Input, b.Input == nil, b.InputError)
			}
		}
	})

	t.Run("unescaped newline inside a string", func(t *testing.T) {
		raw := "{\"path\":\"a.go\",\"old_string\":\"line one\nline two\",\"new_string\":\"z\"}"
		b := NewToolUseBlockFromJSON("c1", "edit_file", raw)
		if b.InputError == "" {
			t.Fatal("expected InputError for a raw newline in a JSON string")
		}
		if b.Input == nil || len(b.Input) != 0 {
			t.Errorf("Input should be an empty map on parse failure, got %v", b.Input)
		}
		if !strings.Contains(b.InputError, "near byte") || !strings.Contains(b.InputError, "line one") {
			t.Errorf("InputError should quote the failing spot: %q", b.InputError)
		}
		if strings.Contains(b.InputError, "truncated") {
			t.Errorf("complete-but-invalid JSON must not be called truncated: %q", b.InputError)
		}
	})

	t.Run("truncated at the token limit", func(t *testing.T) {
		b := NewToolUseBlockFromJSON("c1", "edit_file", `{"path":"a.go","old_string":"func main() {`)
		if b.InputError == "" || !strings.Contains(b.InputError, "truncated") {
			t.Errorf("expected a truncation hint, got %q", b.InputError)
		}
	})

	t.Run("valid JSON but not an object", func(t *testing.T) {
		b := NewToolUseBlockFromJSON("c1", "t", `["a.go"]`)
		if b.InputError == "" {
			t.Error("an array is not a valid argument object")
		}
	})
}

// A tool_use with broken arguments is answered, not executed: the executor is
// never called, the tool_result is an error naming the JSON problem, and the
// turn continues to the model's next reply.
func TestAgent_Run_MalformedToolInput_AnsweredNotExecuted(t *testing.T) {
	bad := NewToolUseBlockFromJSON("call-1", "edit_file", `{"path":"a.go","old_string":"x`)
	send := &fakeToolSender{
		replies: []Reply{
			{StopReason: "tool_use", Blocks: []ContentBlock{bad}},
			{Content: "fixed", StopReason: "end_turn"},
		},
	}
	exec := &fakeExecutor{}
	a := New(send, "m")
	defs := []ToolDefinition{{Name: "edit_file", Description: "edit"}}

	reply, err := a.Run(context.Background(), "edit it", defs, exec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reply.Content != "fixed" {
		t.Errorf("Content = %q", reply.Content)
	}
	if len(exec.called) != 0 {
		t.Errorf("executor must not run a call with malformed arguments; called = %v", exec.called)
	}

	snap := a.History.Snapshot()
	if len(snap) < 3 {
		t.Fatalf("history len = %d", len(snap))
	}
	var result *ContentBlock
	for i := range snap[2].Blocks {
		if snap[2].Blocks[i].Type == "tool_result" && snap[2].Blocks[i].ToolUseID == "call-1" {
			result = &snap[2].Blocks[i]
		}
	}
	if result == nil {
		t.Fatalf("no tool_result for call-1 in %+v", snap[2].Blocks)
	}
	if !result.IsError {
		t.Error("tool_result should be flagged as an error")
	}
	for _, want := range []string{"not valid JSON", "truncated", "Resend"} {
		if !strings.Contains(result.Result, want) {
			t.Errorf("tool_result %q lacks %q", result.Result, want)
		}
	}
	// The tool_use that goes back to the provider must carry an object, not
	// null: an Anthropic-protocol endpoint rejects `"input": null`.
	if snap[1].Blocks[0].Input == nil {
		t.Error("tool_use Input must be a non-nil map after a parse failure")
	}
}
