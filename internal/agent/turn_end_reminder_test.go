package agent

import (
	"context"
	"strings"
	"testing"
)

// reminderTools is a one-tool catalog: Run only enters runLoop (where the
// guard lives) when tools and an executor are present.
var reminderTools = []ToolDefinition{{Name: "noop", Description: "noop"}}

// A non-empty TurnEndReminder buys one more round: the note lands as a user
// message and the model gets to act on it before the turn returns.
func TestRunLoop_TurnEndReminder_InjectsAndContinues(t *testing.T) {
	send := &fakeToolSender{replies: []Reply{
		{Content: "all done", StopReason: "end_turn"},
		{Content: "marked #2 completed", StopReason: "end_turn"},
	}}
	a := New(send, "m")
	calls := 0
	a.TurnEndReminder = func(context.Context) string {
		calls++
		if calls == 1 {
			return "<system-reminder>task #2 still in_progress</system-reminder>"
		}
		return ""
	}

	reply, err := a.Run(context.Background(), "go", reminderTools, &fakeExecutor{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reply.Content != "marked #2 completed" {
		t.Errorf("reply = %q, want the post-reminder reply", reply.Content)
	}
	if send.calls != 2 {
		t.Errorf("sender calls = %d, want 2", send.calls)
	}
	msgs := a.History.Snapshot()
	var injected bool
	for _, m := range msgs {
		if m.Role == RoleUser && strings.Contains(m.Content, "still in_progress") {
			injected = true
		}
	}
	if !injected {
		t.Errorf("reminder was not appended to history: %+v", msgs)
	}
}

// An empty return ends the turn as usual — no extra round-trip.
func TestRunLoop_TurnEndReminder_EmptyEndsTurn(t *testing.T) {
	send := &fakeToolSender{replies: []Reply{{Content: "all done", StopReason: "end_turn"}}}
	a := New(send, "m")
	a.TurnEndReminder = func(context.Context) string { return "" }

	reply, err := a.Run(context.Background(), "go", reminderTools, &fakeExecutor{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reply.Content != "all done" {
		t.Errorf("reply = %q", reply.Content)
	}
	if send.calls != 1 {
		t.Errorf("sender calls = %d, want 1", send.calls)
	}
}

// A model that ignores the reminder must not be able to hold the turn open:
// the guard fires at most once per turn.
func TestRunLoop_TurnEndReminder_FiresOncePerTurn(t *testing.T) {
	send := &fakeToolSender{replies: []Reply{
		{Content: "all done", StopReason: "end_turn"},
		{Content: "still all done", StopReason: "end_turn"},
	}}
	a := New(send, "m")
	fired := 0
	a.TurnEndReminder = func(context.Context) string {
		fired++
		return "<system-reminder>nag</system-reminder>"
	}

	if _, err := a.Run(context.Background(), "go", reminderTools, &fakeExecutor{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fired != 1 {
		t.Errorf("reminder fired %d times, want 1", fired)
	}
	if send.calls != 2 {
		t.Errorf("sender calls = %d, want 2", send.calls)
	}

	// The latch re-arms for the next turn.
	send.replies = append(send.replies,
		Reply{Content: "second turn", StopReason: "end_turn"},
		Reply{Content: "second turn, nagged", StopReason: "end_turn"})
	if _, err := a.Run(context.Background(), "again", reminderTools, &fakeExecutor{}); err != nil {
		t.Fatalf("Run (2nd turn): %v", err)
	}
	if fired != 2 {
		t.Errorf("reminder fired %d times across two turns, want 2", fired)
	}
}

// On the last allowed iteration the guard stands down: spending the final round
// on bookkeeping would turn a clean finish into a max-turns stop.
func TestRunLoop_TurnEndReminder_SkippedAtTurnLimit(t *testing.T) {
	send := &fakeToolSender{replies: []Reply{{Content: "all done", StopReason: "end_turn"}}}
	a := New(send, "m")
	a.MaxTurns = 1
	fired := 0
	a.TurnEndReminder = func(context.Context) string {
		fired++
		return "<system-reminder>nag</system-reminder>"
	}

	reply, err := a.Run(context.Background(), "go", reminderTools, &fakeExecutor{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fired != 0 {
		t.Errorf("reminder fired %d times at the turn limit, want 0", fired)
	}
	if reply.Content != "all done" {
		t.Errorf("reply = %q", reply.Content)
	}
}
