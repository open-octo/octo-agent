package agent

import (
	"context"
	"strings"
	"testing"
)

// reminderTools is a one-tool catalog: Run only enters runLoop (where the
// guard lives) when tools and an executor are present.
var reminderTools = []ToolDefinition{{Name: "noop", Description: "noop"}}

// constReminder is a TurnEndReminder that always asks for one more round.
func constReminder(note string, fired *int) func(context.Context, []string) string {
	return func(context.Context, []string) string {
		*fired++
		return note
	}
}

// A non-empty TurnEndReminder buys one more round: the note lands as a user
// message and the model gets to act on it before the turn returns.
func TestRunLoop_TurnEndReminder_InjectsAndContinues(t *testing.T) {
	send := &fakeToolSender{replies: []Reply{
		{Content: "all done", StopReason: "end_turn"},
		{Content: "marked #2 completed", StopReason: "end_turn"},
	}}
	a := New(send, "m")
	calls := 0
	a.TurnEndReminder = func(context.Context, []string) string {
		calls++
		if calls == 1 {
			return "<system-reminder>task #2 still in_progress</system-reminder>"
		}
		return ""
	}

	if _, err := a.Run(context.Background(), "go", reminderTools, &fakeExecutor{}); err != nil {
		t.Fatalf("Run: %v", err)
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

// The reminder's extra round continues a stretch of assistant speech the user
// is already reading, so the final Reply must carry the pre-reminder answer as
// well — the web transport broadcasts Reply.Content as the settled content of
// that block, and dropping the head would erase the answer on screen.
func TestRunLoop_TurnEndReminder_ReplyKeepsPreReminderAnswer(t *testing.T) {
	send := &fakeToolSender{replies: []Reply{
		{Content: "the answer", StopReason: "end_turn"},
		{Content: "noted", StopReason: "end_turn"},
	}}
	a := New(send, "m")
	fired := 0
	a.TurnEndReminder = onceReminder(&fired)

	reply, err := a.Run(context.Background(), "go", reminderTools, &fakeExecutor{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := "the answer\n\nnoted"; reply.Content != want {
		t.Errorf("Content = %q, want %q", reply.Content, want)
	}
	// History keeps them as the two separate assistant turns they really were.
	var assistants []string
	for _, m := range a.History.Snapshot() {
		if m.Role == RoleAssistant {
			assistants = append(assistants, m.Content)
		}
	}
	if len(assistants) != 2 || assistants[0] != "the answer" || assistants[1] != "noted" {
		t.Errorf("history assistant messages = %q, want the two replies unmerged", assistants)
	}
}

// A tool call in between opens a new block on every UI, so the pre-reminder
// answer is no longer part of what this reply settles — re-attaching it would
// render the answer twice.
func TestRunLoop_TurnEndReminder_ToolCallDropsTheCarry(t *testing.T) {
	send := &fakeToolSender{replies: []Reply{
		{Content: "the answer", StopReason: "end_turn"},
		{StopReason: "tool_use", Blocks: []ContentBlock{{Type: "tool_use", ID: "t1", Name: "noop", Input: map[string]any{}}}},
		{Content: "marked it completed", StopReason: "end_turn"},
	}}
	a := New(send, "m")
	fired := 0
	a.TurnEndReminder = onceReminder(&fired)

	reply, err := a.Run(context.Background(), "go", reminderTools, &fakeExecutor{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reply.Content != "marked it completed" {
		t.Errorf("Content = %q, want only the post-tool reply", reply.Content)
	}
}

// A visible steer lands between the two halves, so they are no longer one
// block on screen and must not be re-joined.
func TestRunLoop_TurnEndReminder_VisibleSteerDropsTheCarry(t *testing.T) {
	send := &fakeToolSender{replies: []Reply{
		{Content: "the answer", StopReason: "end_turn"},
		{Content: "noted", StopReason: "end_turn"},
		{Content: "ok, and about that", StopReason: "end_turn"},
	}}
	a := New(send, "m")
	fired := 0
	a.TurnEndReminder = onceReminder(&fired)
	// The steer arrives while the reminder round is in flight.
	send.onCall = func(idx int) {
		if idx == 1 {
			a.Inbox.Enqueue("wait, one more thing")
		}
	}

	reply, err := a.Run(context.Background(), "go", reminderTools, &fakeExecutor{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(reply.Content, "the answer") {
		t.Errorf("Content = %q, want the carry dropped after a visible steer", reply.Content)
	}
}

// A pure <system-reminder> steer (a background-process completion note) is
// invisible on every surface, so it does not break the block up.
func TestRunLoop_TurnEndReminder_InvisibleSteerKeepsTheCarry(t *testing.T) {
	send := &fakeToolSender{replies: []Reply{
		{Content: "the answer", StopReason: "end_turn"},
		{Content: "noted", StopReason: "end_turn"},
		{Content: "and the background job finished", StopReason: "end_turn"},
	}}
	a := New(send, "m")
	fired := 0
	a.TurnEndReminder = onceReminder(&fired)
	send.onCall = func(idx int) {
		if idx == 1 {
			a.Inbox.Enqueue("<system-reminder>bg job done</system-reminder>")
		}
	}

	reply, err := a.Run(context.Background(), "go", reminderTools, &fakeExecutor{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(reply.Content, "the answer") {
		t.Errorf("Content = %q, want the carried answer kept", reply.Content)
	}
}

// onceReminder fires the reminder exactly on its first call.
func onceReminder(fired *int) func(context.Context, []string) string {
	return func(context.Context, []string) string {
		*fired++
		if *fired == 1 {
			return "<system-reminder>close the plan</system-reminder>"
		}
		return ""
	}
}

// An empty return ends the turn as usual — no extra round-trip.
func TestRunLoop_TurnEndReminder_EmptyEndsTurn(t *testing.T) {
	send := &fakeToolSender{replies: []Reply{{Content: "all done", StopReason: "end_turn"}}}
	a := New(send, "m")
	a.TurnEndReminder = func(context.Context, []string) string { return "" }

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

// The reminder is handed the tools this turn dispatched, so a guard can scope
// itself to turns that touched what it watches.
func TestRunLoop_TurnEndReminder_SeesToolsUsed(t *testing.T) {
	send := &fakeToolSender{replies: []Reply{
		{StopReason: "tool_use", Blocks: []ContentBlock{{Type: "tool_use", ID: "t1", Name: "task_update", Input: map[string]any{}}}},
		{Content: "done", StopReason: "end_turn"},
	}}
	a := New(send, "m")
	var got []string
	a.TurnEndReminder = func(_ context.Context, toolsUsed []string) string {
		got = append([]string(nil), toolsUsed...)
		return ""
	}

	if _, err := a.Run(context.Background(), "go", reminderTools, &fakeExecutor{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got) != 1 || got[0] != "task_update" {
		t.Errorf("toolsUsed = %v, want [task_update]", got)
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
	a.TurnEndReminder = constReminder("<system-reminder>nag</system-reminder>", &fired)

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
	a.TurnEndReminder = constReminder("<system-reminder>nag</system-reminder>", &fired)

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

// A cancelled turn must not spend another provider round-trip: that only widens
// the window in which the interrupt lands on top of an answer already given.
func TestRunLoop_TurnEndReminder_SkippedWhenCancelled(t *testing.T) {
	send := &fakeToolSender{replies: []Reply{{Content: "all done", StopReason: "end_turn"}}}
	a := New(send, "m")
	fired := 0
	a.TurnEndReminder = constReminder("<system-reminder>nag</system-reminder>", &fired)

	ctx, cancel := context.WithCancel(context.Background())
	send.onCall = func(int) { cancel() } // turn is cancelled by the time the reply lands

	if _, err := a.Run(ctx, "go", reminderTools, &fakeExecutor{}); err != nil && err != context.Canceled {
		t.Fatalf("Run: %v", err)
	}
	if fired != 0 {
		t.Errorf("reminder fired %d times on a cancelled turn, want 0", fired)
	}
	if send.calls != 1 {
		t.Errorf("sender calls = %d, want 1", send.calls)
	}
}
