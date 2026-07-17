package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// summarizeFake records the summarization side-call and returns a canned
// summary. It implements only Sender (SendMessages), which is all summarize
// needs.
type summarizeFake struct {
	summary string
	calls   int
	gotMsgs []Message
}

func (f *summarizeFake) SendMessages(_ context.Context, _, _ string, msgs []Message, _ int) (Reply, error) {
	f.calls++
	f.gotMsgs = msgs
	return Reply{Content: f.summary, InputTokens: 5, OutputTokens: 3}, nil
}

func TestSafeSplitIndexByBudget(t *testing.T) {
	u := NewUserMessage
	a := NewAssistantMessage
	// big is ~100 tokens (400 ASCII chars / 4); a u()+a() turn is ~200 tokens.
	big := strings.Repeat("x ", 200)
	bigU := func() Message { return u(big) }
	bigA := func() Message { return a(big) }

	t.Run("fewer than two user turns folds nothing", func(t *testing.T) {
		msgs := []Message{bigU(), bigA()}
		if got := safeSplitIndexByBudget(msgs, 10); got != 0 {
			t.Errorf("split = %d, want 0 (only one user turn)", got)
		}
	})

	t.Run("budget keeps the last N turns that fit", func(t *testing.T) {
		// 6 turns of ~200 tokens each at user indices 0,2,4,6,8,10.
		var msgs []Message
		for i := 0; i < 6; i++ {
			msgs = append(msgs, bigU(), bigA())
		}
		// Budget ~450 tokens fits the last 2 turns (~400) but not 3 (~600),
		// so the split keeps turns from user index 8.
		if got := safeSplitIndexByBudget(msgs, 450); got != 8 {
			t.Errorf("split = %d, want 8 (keep last 2 turns)", got)
		}
	})

	t.Run("a single oversized recent turn is still kept whole", func(t *testing.T) {
		// Last turn alone (~200 tokens) exceeds a tiny budget; we never split
		// inside a turn, so we keep just it (split at its user index).
		var msgs []Message
		for i := 0; i < 3; i++ {
			msgs = append(msgs, bigU(), bigA())
		}
		if got := safeSplitIndexByBudget(msgs, 10); got != 4 {
			t.Errorf("split = %d, want 4 (keep only the last turn)", got)
		}
	})

	t.Run("tool_result messages are not split points", func(t *testing.T) {
		toolResult := Message{Role: RoleUser, Blocks: []ContentBlock{NewToolResultBlock("c1", big, false)}}
		msgs := []Message{
			bigU(),     // 0  user turn
			bigA(),     // 1  asst tool_use
			toolResult, // 2  NOT a user turn
			bigA(),     // 3  asst final
			bigU(),     // 4  user turn
			bigA(),     // 5
		}
		// Tiny budget keeps only the last turn; the split lands on the real
		// user turn at 4, never on the tool_result at 2.
		if got := safeSplitIndexByBudget(msgs, 10); got != 4 {
			t.Errorf("split = %d, want 4 (second real user turn, not the tool_result)", got)
		}
	})
}

func TestContextWindow(t *testing.T) {
	if got := contextWindow("claude-haiku-4-5-20251001"); got != 256_000 {
		t.Errorf("claude window = %d, want 256000", got)
	}
	if got := contextWindow("k2.6"); got != 256_000 {
		t.Errorf("kimi k2.6 window = %d, want 256000", got)
	}
	if got := contextWindow("kimi-for-coding-highspeed"); got != 256_000 {
		t.Errorf("kimi-for-coding-highspeed window = %d, want 256000", got)
	}
	if got := contextWindow("k3"); got != 1_000_000 {
		t.Errorf("k3 window = %d, want 1000000", got)
	}
	if got := contextWindow("deepseek-v4-pro"); got != 1_000_000 {
		t.Errorf("deepseek-v4-pro window = %d, want 1000000", got)
	}
	if got := contextWindow("qwen-3.7-max"); got != 1_000_000 {
		t.Errorf("qwen-3.7-max window = %d, want 1000000", got)
	}
	if got := contextWindow("gpt-5.5"); got != 1_000_000 {
		t.Errorf("gpt-5.5 window = %d, want 1000000", got)
	}
	if got := contextWindow("gemini-3.5-flash"); got != 1_000_000 {
		t.Errorf("gemini-3.5-flash window = %d, want 1000000", got)
	}
	if got := contextWindow("LongCat-2.0"); got != 1_000_000 {
		t.Errorf("LongCat-2.0 window = %d, want 1000000", got)
	}
	if got := contextWindow("claude-sonnet-5"); got != 1_000_000 {
		t.Errorf("claude-sonnet-5 window = %d, want 1000000", got)
	}
	if got := contextWindow("claude-fable-5"); got != 1_000_000 {
		t.Errorf("claude-fable-5 window = %d, want 1000000", got)
	}
	if got := contextWindow("gpt-5.6-sol"); got != 1_000_000 {
		t.Errorf("gpt-5.6-sol window = %d, want 1000000", got)
	}
	if got := contextWindow("glm-5.2"); got != 1_000_000 {
		t.Errorf("glm-5.2 window = %d, want 1000000", got)
	}
	if got := contextWindow("x-ai/grok-4.5"); got != 500_000 {
		t.Errorf("grok-4.5 window = %d, want 500000", got)
	}
	if got := contextWindow("xiaomi/mimo-v2.5"); got != 1_000_000 {
		t.Errorf("mimo-v2.5 window = %d, want 1000000", got)
	}
	if got := contextWindow("minimax/minimax-m3"); got != 1_000_000 {
		t.Errorf("minimax-m3 window = %d, want 1000000", got)
	}
	if got := contextWindow("tencent/hy3"); got != 256_000 {
		t.Errorf("hy3 window = %d, want 256000", got)
	}
	if got := contextWindow("nvidia/nemotron-3-ultra-550b-a55b"); got != 1_000_000 {
		t.Errorf("nemotron-3-ultra window = %d, want 1000000", got)
	}
	if got := contextWindow("unknown-model-xyz"); got != defaultContextWindow {
		t.Errorf("unknown model window = %d, want %d (conservative default)", got, defaultContextWindow)
	}
}

func TestCompactTriggerTokens(t *testing.T) {
	a := New(&summarizeFake{}, "unknown-model-xyz") // unknown model → default window

	a.CompactThreshold = -1
	if got := a.compactTriggerTokens(); got != 0 {
		t.Errorf("disabled trigger = %d, want 0", got)
	}

	a.CompactThreshold = 0 // auto
	want := int(float64(defaultContextWindow) * compactThresholdFraction)
	if got := a.compactTriggerTokens(); got != want {
		t.Errorf("auto trigger = %d, want %d", got, want)
	}

	a.CompactThreshold = 4242 // explicit
	if got := a.compactTriggerTokens(); got != 4242 {
		t.Errorf("explicit trigger = %d, want 4242", got)
	}

	// Custom auto fraction (50%).
	a.CompactThreshold = 0
	a.CompactAutoFraction = 0.5
	want50 := int(float64(defaultContextWindow) * 0.5)
	if got := a.compactTriggerTokens(); got != want50 {
		t.Errorf("custom 50%% trigger = %d, want %d", got, want50)
	}

	// Fraction clamped to 1.0 when > 1.
	a.CompactAutoFraction = 2.0
	want100 := defaultContextWindow
	if got := a.compactTriggerTokens(); got != want100 {
		t.Errorf("clamped 200%% trigger = %d, want %d", got, want100)
	}
}

// Between-batches (mid-turn) compaction uses the one and only compaction
// trigger — the same point between-turns fires — so a single threshold governs
// the whole session and honors CompactAutoFraction.
func TestShouldCompactBetweenBatches_UsesSingleTrigger(t *testing.T) {
	a := New(&summarizeFake{}, "unknown-model-xyz") // unknown model → default window

	trigger := a.compactTriggerTokens() // auto: 75% of the default window
	// Just under the trigger: no mid-turn compaction.
	a.usageMu.Lock()
	a.lastInputTokens = trigger - 1
	a.usageMu.Unlock()
	if a.shouldCompactBetweenBatches() {
		t.Errorf("should not compact between batches below the trigger (%d)", trigger)
	}
	// Just over: it fires, at the very same threshold between-turns uses.
	a.usageMu.Lock()
	a.lastInputTokens = trigger + 1
	a.usageMu.Unlock()
	if !a.shouldCompactBetweenBatches() {
		t.Errorf("should compact between batches above the trigger (%d)", trigger)
	}

	// Disabling compaction entirely (CompactThreshold < 0) disables it here too.
	a.CompactThreshold = -1
	if a.shouldCompactBetweenBatches() {
		t.Error("should not compact between batches when compaction is disabled")
	}
}

// TestMaybeCompact_AutoDefaultTriggers proves the C6 flip: with CompactThreshold
// left at 0, a context past the window-relative trigger compacts automatically.
func TestMaybeCompact_AutoDefaultTriggers(t *testing.T) {
	f := &summarizeFake{summary: "S"}
	a := New(f, "unknown-model-xyz") // CompactThreshold defaults to 0 (auto), unknown model → default window
	trigger := int(float64(defaultContextWindow) * compactThresholdFraction)
	// Build a history whose estimated token count exceeds the trigger.
	longMsg := strings.Repeat("x ", 500) // ~250 tokens each
	for estimateMessages(a.History.Snapshot()) <= trigger {
		a.History.Append(NewUserMessage(longMsg))
		a.History.Append(NewAssistantMessage(longMsg))
	}
	if err := a.maybeCompact(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 {
		t.Errorf("auto default should compact once; calls=%d", f.calls)
	}
}

func TestMaybeCompact_DisabledOrBelowThreshold(t *testing.T) {
	f := &summarizeFake{summary: "S"}
	a := New(f, "m")
	longMsg := strings.Repeat("x ", 500) // ~250 tokens each
	for i := 0; i < 6; i++ {
		a.History.Append(NewUserMessage(longMsg))
		a.History.Append(NewAssistantMessage(longMsg))
	}

	// Disabled (negative threshold) — even a huge context must not compact.
	a.CompactThreshold = -1
	if err := a.maybeCompact(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if f.calls != 0 || a.History.Len() != 12 {
		t.Errorf("disabled: should not compact; calls=%d len=%d", f.calls, a.History.Len())
	}

	// Enabled but history still under threshold.
	a.CompactThreshold = 100000 // way above the ~1500 tokens of the 12 messages
	if err := a.maybeCompact(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if f.calls != 0 || a.History.Len() != 12 {
		t.Errorf("under threshold: should not compact; calls=%d len=%d", f.calls, a.History.Len())
	}
}

// When the token bulk is wedged inside the most recent turn (a single huge
// agentic turn), folding the older sliver wouldn't help, so maybeCompact must
// skip the summarize call rather than thrash — reclamation handles that case.
func TestMaybeCompact_AntiThrashSkipsSliverFold(t *testing.T) {
	f := &summarizeFake{summary: "S"}
	a := New(f, "m")
	a.CompactThreshold = 100

	// Turn 0 is tiny; turn 1 carries all the bulk. The split keeps the huge
	// recent turn (can't fold inside it) and would fold only the tiny prefix.
	a.History.Append(NewUserMessage("hi"))
	a.History.Append(NewAssistantMessage("ok"))
	a.History.Append(NewUserMessage("go"))
	a.History.Append(NewAssistantMessage(strings.Repeat("x ", 5000))) // ~2500 tokens

	if err := a.maybeCompact(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if f.calls != 0 {
		t.Errorf("anti-thrash should skip the summarize; calls=%d", f.calls)
	}
	if a.History.Len() != 4 {
		t.Errorf("history must be untouched; len=%d, want 4", a.History.Len())
	}
}

func TestMaybeCompact_RewritesHistory(t *testing.T) {
	f := &summarizeFake{summary: "GOAL: build X. Touched main.go."}
	a := New(f, "m")
	a.CompactThreshold = 100

	longMsg := strings.Repeat("x ", 500) // ~250 tokens each
	for i := 0; i < 6; i++ {
		a.History.Append(NewUserMessage(longMsg))
		a.History.Append(NewAssistantMessage(longMsg))
	}
	// History now well over the 100-token threshold.

	if err := a.maybeCompact(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	snap := a.History.Snapshot()
	// First message is the summary user-message.
	if snap[0].Role != RoleUser || !strings.Contains(snap[0].Content, "GOAL: build X") {
		t.Errorf("first message should carry the summary, got %+v", snap[0])
	}
	// With an explicit trigger of 100, keepBudget is capped at trigger/2 = 50
	// tokens — smaller than one ~500-token turn — so only the most recent user
	// turn (2 messages) is kept. Split at index 10; kept = msgs[10:] = 2
	// messages, + 1 summary = 3.
	if len(snap) != 3 {
		t.Errorf("history len = %d, want 3", len(snap))
	}
	// The summarized side-call saw the dropped prefix (10 msgs) + 1 instruction.
	if f.calls != 1 {
		t.Fatalf("summarize calls = %d, want 1", f.calls)
	}
	if len(f.gotMsgs) != 11 {
		t.Errorf("summarize saw %d messages, want 11 (10 dropped + instruction)", len(f.gotMsgs))
	}
	// The most-recent turn survived verbatim.
	if snap[len(snap)-1].Content != longMsg {
		t.Errorf("last message = %q, want %q", snap[len(snap)-1].Content, longMsg)
	}
	// Trigger reset so we don't immediately re-compact.
	if a.lastInputTokens != 0 {
		t.Errorf("lastInputTokens should reset to 0, got %d", a.lastInputTokens)
	}
}

// TestForceCompact_FoldsBelowAutoThreshold proves /compact folds even when the
// context sits below the auto-compaction trigger (where maybeCompact no-ops).
func TestForceCompact_FoldsBelowAutoThreshold(t *testing.T) {
	f := &summarizeFake{summary: "GOAL: build X."}
	a := New(f, "m") // unknown model → 128k window; keepBudget caps at trigger/2
	a.CompactThreshold = 4000

	longMsg := strings.Repeat("x ", 500) // ~250 tokens each → ~3000 total
	for i := 0; i < 6; i++ {
		a.History.Append(NewUserMessage(longMsg))
		a.History.Append(NewAssistantMessage(longMsg))
	}
	before := len(a.History.Snapshot())

	// Below the 4000 trigger: the auto path must leave history untouched.
	if err := a.maybeCompact(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got := len(a.History.Snapshot()); got != before {
		t.Fatalf("maybeCompact should no-op below the trigger; len %d → %d", before, got)
	}

	// The explicit /compact must fold anyway.
	stats, err := a.ForceCompact(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FoldedMsgs == 0 {
		t.Fatal("ForceCompact should have folded the older turns")
	}
	snap := a.History.Snapshot()
	if len(snap) >= before {
		t.Errorf("history not reduced: %d → %d", before, len(snap))
	}
	if snap[0].Role != RoleUser || !strings.Contains(snap[0].Content, "GOAL: build X") {
		t.Errorf("first message should carry the summary, got %+v", snap[0])
	}
	if f.calls != 1 {
		t.Errorf("summarize calls = %d, want 1", f.calls)
	}
	if a.lastInputTokens != 0 {
		t.Errorf("trigger should reset to 0 after compaction, got %d", a.lastInputTokens)
	}
}

// TestForceCompact_NoOpOnTinyHistory: nothing foldable (one turn) yields a
// no-op stats result with history untouched.
func TestForceCompact_NoOpOnTinyHistory(t *testing.T) {
	f := &summarizeFake{summary: "unused"}
	a := New(f, "m")
	a.History.Append(NewUserMessage("hello"))
	a.History.Append(NewAssistantMessage("hi"))

	stats, err := a.ForceCompact(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FoldedMsgs != 0 || stats.ReclaimedTokens != 0 {
		t.Errorf("expected a no-op, got %+v", stats)
	}
	if f.calls != 0 {
		t.Errorf("summarize should not be called on a no-op, got %d", f.calls)
	}
	if len(a.History.Snapshot()) != 2 {
		t.Errorf("history must be untouched, got %d messages", len(a.History.Snapshot()))
	}
}

// TestClearHistory wipes the conversation and resets the context gauge while
// keeping the system prompt.
func TestClearHistory(t *testing.T) {
	a := New(&summarizeFake{}, "m")
	a.System = "you are a test"
	a.History.Append(NewUserMessage("hello"))
	a.History.Append(NewAssistantMessage("hi"))
	a.lastInputTokens = 1234

	a.ClearHistory()

	if got := len(a.History.Snapshot()); got != 0 {
		t.Errorf("history len = %d, want 0", got)
	}
	if a.lastInputTokens != 0 {
		t.Errorf("context gauge not reset: %d", a.lastInputTokens)
	}
	if a.System != "you are a test" {
		t.Errorf("system prompt must survive /clear, got %q", a.System)
	}
}

// streamingSummarizeFake implements StreamingSender, emitting the canned
// summary in chunks so the compaction-progress events can be observed.
type streamingSummarizeFake struct {
	summary string
	chunks  int
}

func (f *streamingSummarizeFake) SendMessages(_ context.Context, _, _ string, _ []Message, _ int) (Reply, error) {
	return Reply{Content: f.summary}, nil
}

func (f *streamingSummarizeFake) StreamMessages(_ context.Context, _, _ string, _ []Message, _ int, onChunk func(string), _ func(string)) (Reply, error) {
	// Emit one rune at a time so SummaryTokens grows monotonically across events.
	for _, r := range f.summary {
		f.chunks++
		onChunk(string(r))
	}
	return Reply{Content: f.summary, InputTokens: 5, OutputTokens: 3}, nil
}

// TestMaybeCompact_EmitsProgressEvents proves compaction surfaces a
// started → progress(streaming) → done event sequence with sane stats, so the
// TUI can show a live "compacting conversation history" indicator.
func TestMaybeCompact_EmitsProgressEvents(t *testing.T) {
	f := &streamingSummarizeFake{summary: "GOAL build X. Touched main.go."}
	a := New(f, "m")
	a.CompactThreshold = 100

	longMsg := strings.Repeat("x ", 500) // ~250 tokens each
	for i := 0; i < 6; i++ {
		a.History.Append(NewUserMessage(longMsg))
		a.History.Append(NewAssistantMessage(longMsg))
	}

	var events []AgentEvent
	handler := func(ev AgentEvent) { events = append(events, ev) }

	if err := a.maybeCompact(context.Background(), handler); err != nil {
		t.Fatal(err)
	}

	if len(events) < 3 {
		t.Fatalf("expected started + progress + done events, got %d", len(events))
	}

	// First event: started, with the pre-compaction estimate and fold counts.
	first := events[0]
	if first.Kind != EventCompactStarted {
		t.Errorf("first event = %q, want compact_started", first.Kind)
	}
	if first.Compact == nil || first.Compact.BeforeTokens <= 0 {
		t.Errorf("started event missing BeforeTokens: %+v", first.Compact)
	}
	if first.Compact.KeptTurns != 1 {
		t.Errorf("started KeptTurns = %d, want 1 (tiny budget keeps one turn)", first.Compact.KeptTurns)
	}
	if first.Compact.MaxTokens != summarizeMaxTokens {
		t.Errorf("started MaxTokens = %d, want %d", first.Compact.MaxTokens, summarizeMaxTokens)
	}

	// Middle: at least one progress event whose summary estimate is monotonic.
	var progress []AgentEvent
	prevTokens := -1
	for _, ev := range events {
		if ev.Kind != EventCompactProgress {
			continue
		}
		progress = append(progress, ev)
		if ev.Chunk == "" {
			t.Errorf("progress event missing Chunk")
		}
		if ev.Compact == nil || ev.Compact.SummaryTokens < prevTokens {
			t.Errorf("progress SummaryTokens not monotonic: %+v (prev=%d)", ev.Compact, prevTokens)
		}
		prevTokens = ev.Compact.SummaryTokens
	}
	if len(progress) == 0 {
		t.Errorf("expected at least one compact_progress event")
	}

	// Last event: done, with after < before (a real reduction happened).
	last := events[len(events)-1]
	if last.Kind != EventCompactDone {
		t.Errorf("last event = %q, want compact_done", last.Kind)
	}
	if last.Compact == nil || last.Compact.AfterTokens <= 0 || last.Compact.AfterTokens >= last.Compact.BeforeTokens {
		t.Errorf("done event should show reduction: %+v", last.Compact)
	}
}

// compactingFake serves the summarization side-call (SendMessages) and the
// agentic loop (SendMessagesWithTools) from separate scripts, so a Run can be
// observed triggering compaction before its turn.
type compactingFake struct {
	summary        string
	loopReplies    []Reply
	loopCalls      int
	summarizeCalls int
}

func (f *compactingFake) SendMessages(_ context.Context, _, _ string, _ []Message, _ int) (Reply, error) {
	f.summarizeCalls++
	return Reply{Content: f.summary}, nil
}

func (f *compactingFake) SendMessagesWithTools(_ context.Context, _, _ string, _ []Message, _ int, _ []ToolDefinition) (Reply, error) {
	r := f.loopReplies[f.loopCalls]
	f.loopCalls++
	return r, nil
}

func TestRun_CompactsBetweenTurns(t *testing.T) {
	f := &compactingFake{
		summary:     "earlier work summary",
		loopReplies: []Reply{{Content: "done", StopReason: "end_turn"}},
	}
	a := New(f, "m")
	a.CompactThreshold = 100

	// Seed a long prior history whose estimated size exceeds the threshold.
	longMsg := strings.Repeat("x ", 500) // ~250 tokens each
	for i := 0; i < 6; i++ {
		a.History.Append(NewUserMessage(longMsg))
		a.History.Append(NewAssistantMessage(longMsg))
	}

	defs := []ToolDefinition{{Name: "terminal"}}
	exec := &fakeExecutor{}
	if _, err := a.Run(context.Background(), "next request", defs, exec); err != nil {
		t.Fatal(err)
	}

	if f.summarizeCalls != 1 {
		t.Errorf("expected one summarization call before the turn, got %d", f.summarizeCalls)
	}
	// History now: summary + 8 kept + new user("next request") + assistant("done") = 11.
	snap := a.History.Snapshot()
	if !strings.Contains(snap[0].Content, "earlier work summary") {
		t.Errorf("compaction summary should lead the history, got %+v", snap[0])
	}
}

// failingSender always errors — used to prove the lite→primary fallback.
type failingSender struct{ calls int }

func (f *failingSender) SendMessages(_ context.Context, _, _ string, _ []Message, _ int) (Reply, error) {
	f.calls++
	return Reply{}, fmt.Errorf("lite endpoint down")
}

// modelRecordingFake records which model each call was made with.
type modelRecordingFake struct {
	summary string
	models  []string
}

func (f *modelRecordingFake) SendMessages(_ context.Context, model, _ string, _ []Message, _ int) (Reply, error) {
	f.models = append(f.models, model)
	return Reply{Content: f.summary}, nil
}

// msgCountRecordingFake records how many messages the last call received.
type msgCountRecordingFake struct {
	summary string
	lastN   int
}

func (f *msgCountRecordingFake) SendMessages(_ context.Context, _, _ string, msgs []Message, _ int) (Reply, error) {
	f.lastN = len(msgs)
	return Reply{Content: f.summary}, nil
}

// TestSummarizeOn_OverflowMeasuresSlice guards against measuring the overflow
// loop with the full-history token count instead of the slice being summarised.
// With lastInputTokens set high (the primary model's ~75%-of-window prompt
// count) and a smaller summarising window, the loop must NOT pop a slice that
// already fits — doing so would discard nearly the whole chunk.
func TestSummarizeOn_OverflowMeasuresSlice(t *testing.T) {
	lite := &msgCountRecordingFake{summary: "s"}
	a := New(&modelRecordingFake{summary: "p"}, "claude-opus-4")
	// Simulate the provider having reported a huge full-history prompt size.
	a.lastInputTokens = 5_000_000 // >> defaultContextWindow (128k)

	// A small slice that easily fits the summarising window.
	msgs := []Message{
		NewUserMessage("a"), NewAssistantMessage("b"),
		NewUserMessage("c"), NewAssistantMessage("d"),
		NewUserMessage("e"), NewAssistantMessage("f"),
	}
	if _, err := a.summarizeOn(context.Background(), lite, "lite-model", msgs, nil); err != nil {
		t.Fatal(err)
	}
	// summarizeOn appends one compression prompt, so a slice that fits yields
	// len(msgs)+1 messages. The bug popped the slice down to 2 (sending 3).
	if lite.lastN < len(msgs)+1 {
		t.Errorf("summariser received %d messages; the overflow loop wrongly popped a fitting slice (want %d). lastInputTokens must not size a sub-slice.", lite.lastN, len(msgs)+1)
	}
}

func TestSummarize_UsesLiteModelWhenSet(t *testing.T) {
	primary := &modelRecordingFake{summary: "primary summary"}
	lite := &modelRecordingFake{summary: "lite summary"}

	a := New(primary, "big-model")
	a.LiteSender = lite
	a.LiteModel = "lite-model"

	msgs := []Message{NewUserMessage("hi"), NewAssistantMessage("hello")}
	sum, err := a.summarize(context.Background(), msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum != "lite summary" {
		t.Errorf("summary = %q, want the lite sender's", sum)
	}
	if len(lite.models) != 1 || lite.models[0] != "lite-model" {
		t.Errorf("lite calls = %v, want one call with lite-model", lite.models)
	}
	if len(primary.models) != 0 {
		t.Errorf("primary sender called %v times, want 0", len(primary.models))
	}
}

func TestSummarize_FallsBackToPrimaryOnLiteError(t *testing.T) {
	primary := &modelRecordingFake{summary: "primary summary"}
	lite := &failingSender{}

	a := New(primary, "big-model")
	a.LiteSender = lite
	a.LiteModel = "lite-model"

	msgs := []Message{NewUserMessage("hi"), NewAssistantMessage("hello")}
	sum, err := a.summarize(context.Background(), msgs, nil)
	if err != nil {
		t.Fatalf("summarize must survive a lite failure: %v", err)
	}
	if sum != "primary summary" {
		t.Errorf("summary = %q, want the primary fallback", sum)
	}
	if lite.calls != 1 {
		t.Errorf("lite calls = %d, want 1", lite.calls)
	}
	if len(primary.models) != 1 || primary.models[0] != "big-model" {
		t.Errorf("primary calls = %v, want one call with big-model", primary.models)
	}
}

func TestSummarize_NoLiteConfiguredUsesPrimary(t *testing.T) {
	primary := &modelRecordingFake{summary: "primary summary"}
	a := New(primary, "big-model")

	msgs := []Message{NewUserMessage("hi"), NewAssistantMessage("hello")}
	sum, err := a.summarize(context.Background(), msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum != "primary summary" || len(primary.models) != 1 {
		t.Errorf("got %q after %d calls; want primary summary after 1", sum, len(primary.models))
	}
}
