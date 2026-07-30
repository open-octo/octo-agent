package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A first-round failure undoes the user message, so InputRolledBack must say so
// — it is the only way a UI that cleared its input box on send can tell that the
// text it dropped is now gone everywhere.
func TestInputRolledBack_FirstRoundError(t *testing.T) {
	send := &fakeToolSender{errs: []error{errors.New("provider boom")}}
	a := New(send, "m")
	a.History.Append(NewUserMessage("earlier"))
	a.History.Append(NewAssistantMessage("earlier answer"))
	before := a.History.Len()

	if _, err := a.RunStream(context.Background(), "the prompt", nil, nil, nil); err == nil {
		t.Fatal("want an error from the failing sender")
	}
	if !a.InputRolledBack() {
		t.Error("InputRolledBack() = false, want true — the user message was popped")
	}
	if a.History.Len() != before {
		t.Errorf("history len = %d, want %d (pre-turn state)", a.History.Len(), before)
	}
}

// A failure after the first round keeps the user message (and the completed
// round) in history, so nothing was lost and the flag must stay false —
// otherwise a UI would refill its composer with a message still in the
// transcript and the user would send it twice.
func TestInputRolledBack_MidTurnError(t *testing.T) {
	send := &fakeToolSender{
		replies: []Reply{{
			Blocks:     []ContentBlock{NewToolUseBlock("tu1", "terminal", map[string]any{})},
			StopReason: "tool_use",
		}},
		errs: []error{nil, errors.New("provider boom")},
	}
	a := New(send, "m")
	defs := []ToolDefinition{{Name: "terminal"}}

	if _, err := a.RunStream(context.Background(), "run it", defs, &fakeExecutor{}, nil); err == nil {
		t.Fatal("want an error from the second round")
	}
	if a.InputRolledBack() {
		t.Error("InputRolledBack() = true, want false — the message survived the turn")
	}
	if !historyHasUserText(a, "run it") {
		t.Error("the user message should still be in history after a mid-turn failure")
	}
}

// The regression that a history-length comparison gets wrong: a turn that
// compacts at its start and then fails mid-turn ends up with FEWER messages
// than it began with, while the user message is untouched. Inferring the
// rollback from that shrink would hand the composer a message that is still in
// the transcript.
func TestInputRolledBack_CompactionThenMidTurnErrorIsNotRollback(t *testing.T) {
	long := strings.Repeat("x ", 500) // ~250 tokens each
	send := &fakeToolSender{
		replies: []Reply{
			{Content: "SUMMARY"}, // consumed by maybeCompact's summarize call
			{
				Blocks:     []ContentBlock{NewToolUseBlock("tu1", "terminal", map[string]any{})},
				StopReason: "tool_use",
			},
		},
		errs: []error{nil, nil, errors.New("provider boom")},
	}
	a := New(send, "m")
	a.CompactThreshold = 100
	for i := 0; i < 6; i++ {
		a.History.Append(NewUserMessage(long))
		a.History.Append(NewAssistantMessage(long))
	}
	watermark := a.History.Len() + 1 // what the server counts before the turn

	if _, err := a.RunStream(context.Background(), "run it", []ToolDefinition{{Name: "terminal"}}, &fakeExecutor{}, nil); err == nil {
		t.Fatal("want an error from the round after the tool call")
	}
	if send.calls < 3 {
		t.Fatalf("sender calls = %d, want 3 (summarize, tool round, failing round) — the setup didn't compact", send.calls)
	}
	if a.History.Len() >= watermark {
		t.Fatalf("history len = %d, want < %d — this test only means something when the turn shrank history", a.History.Len(), watermark)
	}
	if a.InputRolledBack() {
		t.Error("InputRolledBack() = true, want false — compaction shrank history but kept the user message")
	}
	if !historyHasUserText(a, "run it") {
		t.Error("the user message should still be in history")
	}
}

// The flag is per-turn: a failed turn must not leave it set for the next one.
func TestInputRolledBack_ResetByNextTurn(t *testing.T) {
	send := &fakeToolSender{
		replies: []Reply{{}, {Content: "fine", StopReason: "end_turn"}},
		errs:    []error{errors.New("provider boom")},
	}
	a := New(send, "m")

	if _, err := a.RunStream(context.Background(), "first", nil, nil, nil); err == nil {
		t.Fatal("want an error on the first turn")
	}
	if !a.InputRolledBack() {
		t.Fatal("the failed turn should have set the flag")
	}
	if _, err := a.RunStream(context.Background(), "second", nil, nil, nil); err != nil {
		t.Fatalf("second turn: %v", err)
	}
	if a.InputRolledBack() {
		t.Error("InputRolledBack() = true after a clean turn, want false")
	}
}

func historyHasUserText(a *Agent, want string) bool {
	for _, m := range a.History.Snapshot() {
		if m.Role == RoleUser && strings.Contains(m.Content, want) {
			return true
		}
	}
	return false
}
