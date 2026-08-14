package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// perCallStreamingSender streams a scripted set of text chunks on EVERY call
// (indexed by call number), unlike fakeToolStreamingSender which replays only
// on the first. Needed to exercise the per-round reset of the partial-reply
// accumulator: round N's failure must persist round N's text only.
type perCallStreamingSender struct {
	fakeToolSender
	chunksPerCall [][]string
}

func (f *perCallStreamingSender) StreamMessagesWithTools(
	_ context.Context, _, _ string, _ []Message, _ int,
	_ []ToolDefinition,
	onChunk func(string),
	_ ToolInputDeltaFunc,
	_ ThinkingDeltaFunc,
) (Reply, error) {
	if f.calls < len(f.chunksPerCall) && onChunk != nil {
		for _, c := range f.chunksPerCall[f.calls] {
			onChunk(c)
		}
	}
	return f.nextReply()
}

// TestRunStream_PartialReplyKeptOnFirstRoundError: a first-round send that
// streamed text before dying must persist that text as a partial reply and
// must NOT roll the user message back — the words the user watched arrive
// would otherwise vanish from history (and, after the rollback-triggered
// transcript reload, from the screen too).
func TestRunStream_PartialReplyKeptOnFirstRoundError(t *testing.T) {
	send := &perCallStreamingSender{
		fakeToolSender: fakeToolSender{errs: []error{errors.New("boom")}},
		chunksPerCall:  [][]string{{"Hello, ", "wor"}},
	}
	a := New(send, "m")

	if _, err := a.RunStream(context.Background(), "hi", dummyOverflowTools, &fakeExecutor{}, nil); err == nil {
		t.Fatal("expected the turn to fail")
	}

	msgs := a.History.Snapshot()
	if len(msgs) != 2 {
		t.Fatalf("history has %d messages, want 2 (user + partial assistant)", len(msgs))
	}
	last := msgs[1]
	if last.Role != RoleAssistant {
		t.Fatalf("last message role = %q, want assistant", last.Role)
	}
	if !strings.Contains(last.Content, "Hello, wor") {
		t.Errorf("partial reply text missing, got: %q", last.Content)
	}
	if !strings.Contains(last.Content, partialReplyNote) {
		t.Errorf("partial reply is not marked incomplete, got: %q", last.Content)
	}
	if a.InputRolledBack() {
		t.Error("input must not be reported rolled back when the partial reply was kept")
	}
}

// TestRunStream_NoPartialStillRollsBackFirstRound pins the pre-existing
// contract: a first-round failure that streamed nothing rolls history back
// past the user message so a retry doesn't duplicate it.
func TestRunStream_NoPartialStillRollsBackFirstRound(t *testing.T) {
	send := &perCallStreamingSender{
		fakeToolSender: fakeToolSender{errs: []error{errors.New("boom")}},
	}
	a := New(send, "m")

	if _, err := a.RunStream(context.Background(), "hi", dummyOverflowTools, &fakeExecutor{}, nil); err == nil {
		t.Fatal("expected the turn to fail")
	}
	if n := a.History.Len(); n != 0 {
		t.Errorf("history has %d messages, want 0 (rolled back)", n)
	}
	if !a.InputRolledBack() {
		t.Error("input_rolled_back must stay true when there was no partial to keep")
	}
}

// TestRunStream_PartialReplyKeptOnLaterRoundError: when round 1 dies, only
// round 1's streamed text is persisted — the accumulator must reset between
// rounds, or round 0's (already appended) text would be duplicated into the
// partial.
func TestRunStream_PartialReplyKeptOnLaterRoundError(t *testing.T) {
	send := &perCallStreamingSender{
		fakeToolSender: fakeToolSender{
			replies: []Reply{{
				StopReason: "tool_use",
				Content:    "round zero text",
				Blocks:     []ContentBlock{NewToolUseBlock("c1", "bash", map[string]any{"command": "ls"})},
			}},
			errs: []error{nil, errors.New("boom")},
		},
		chunksPerCall: [][]string{{"round zero text"}, {"round one partial"}},
	}
	a := New(send, "m")

	if _, err := a.RunStream(context.Background(), "hi", dummyOverflowTools, &fakeExecutor{}, nil); err == nil {
		t.Fatal("expected the turn to fail")
	}

	msgs := a.History.Snapshot()
	last := msgs[len(msgs)-1]
	if last.Role != RoleAssistant {
		t.Fatalf("last message role = %q, want assistant", last.Role)
	}
	if !strings.Contains(last.Content, "round one partial") {
		t.Errorf("round 1 partial missing, got: %q", last.Content)
	}
	if strings.Contains(last.Content, "round zero text") {
		t.Errorf("round 0 text leaked into the partial (accumulator not reset): %q", last.Content)
	}
	if !strings.Contains(last.Content, partialReplyNote) {
		t.Errorf("partial reply is not marked incomplete, got: %q", last.Content)
	}
	if a.InputRolledBack() {
		t.Error("a later-round failure must never report the input rolled back")
	}
}

// TestRunStream_TruncatedTextKeptWhenEscalationFails: the accumulator is
// reset before the escalated retry, but the truncated first attempt streamed
// a complete text the user watched arrive — when the retry dies having
// streamed nothing, that snapshot must be the partial that gets kept.
func TestRunStream_TruncatedTextKeptWhenEscalationFails(t *testing.T) {
	send := &perCallStreamingSender{
		fakeToolSender: fakeToolSender{
			replies: []Reply{{Content: "truncated answer", StopReason: StopReasonMaxTokens}},
			errs:    []error{nil, errors.New("boom")},
		},
		chunksPerCall: [][]string{{"truncated answer"}}, // escalated retry streams nothing
	}
	a := New(send, "m")
	a.MaxTokens = 100
	a.MaxTokensEscalate = 200

	if _, err := a.RunStream(context.Background(), "hi", dummyOverflowTools, &fakeExecutor{}, nil); err == nil {
		t.Fatal("expected the escalated turn to fail")
	}

	msgs := a.History.Snapshot()
	last := msgs[len(msgs)-1]
	if last.Role != RoleAssistant {
		t.Fatalf("last message role = %q, want assistant", last.Role)
	}
	if !strings.Contains(last.Content, "truncated answer") {
		t.Errorf("truncated attempt's text was not kept, got: %q", last.Content)
	}
	if !strings.Contains(last.Content, partialReplyNote) {
		t.Errorf("kept text is not marked incomplete, got: %q", last.Content)
	}
	if a.InputRolledBack() {
		t.Error("input must not be rolled back when the truncated text was kept")
	}
}
