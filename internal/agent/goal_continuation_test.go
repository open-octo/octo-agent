package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGoalContinuation_OnlyActiveGoalsContinue(t *testing.T) {
	s := NewSession("m", "")
	if _, ok := s.GoalContinuation(); ok {
		t.Error("no goal must not continue")
	}

	if _, err := s.CreateGoal("ship it", 0); err != nil {
		t.Fatal(err)
	}
	prompt, ok := s.GoalContinuation()
	if !ok {
		t.Fatal("active goal should continue")
	}
	if !strings.HasPrefix(prompt, "<goal_context>") || !strings.HasSuffix(prompt, "</goal_context>") {
		t.Errorf("continuation prompt must be goal_context-wrapped, got %q…", prompt[:40])
	}
	if !strings.Contains(prompt, "ship it") {
		t.Error("continuation prompt must carry the objective")
	}

	if _, err := s.SetGoalStatus(GoalPaused); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.GoalContinuation(); ok {
		t.Error("paused goal must not continue")
	}
}

func TestGoalContinuation_ZeroProgressSuppresses(t *testing.T) {
	s := NewSession("m", "")
	if _, err := s.CreateGoal("g", 0); err != nil {
		t.Fatal(err)
	}

	if _, ok := s.GoalContinuation(); !ok {
		t.Fatal("first continuation should fire")
	}
	if !s.GoalContinuationPending() {
		t.Error("hand-out should mark the continuation pending")
	}

	// The continuation turn accounted nothing: the audit at the next call
	// suppresses further continuations.
	if _, ok := s.GoalContinuation(); ok {
		t.Fatal("zero-progress continuation turn must suppress the next one")
	}
	if s.GoalContinuationPending() {
		t.Error("audit should clear the pending mark")
	}
	if _, ok := s.GoalContinuation(); ok {
		t.Error("suppression should hold on subsequent calls")
	}

	// Real token progress re-arms the loop.
	s.ResetGoalWallClock()
	if _, changed := s.AccountGoalUsage(50); !changed {
		t.Fatal("accounting should register progress")
	}
	if _, ok := s.GoalContinuation(); !ok {
		t.Error("token progress must clear the zero-progress suppression")
	}
}

func TestGoalContinuation_ProgressKeepsLooping(t *testing.T) {
	s := NewSession("m", "")
	if _, err := s.CreateGoal("g", 0); err != nil {
		t.Fatal(err)
	}
	s.ResetGoalWallClock()

	for i := 0; i < 3; i++ {
		if _, ok := s.GoalContinuation(); !ok {
			t.Fatalf("round %d: continuation should fire", i)
		}
		// The continuation turn makes real progress each round.
		s.AccountGoalUsage(100)
	}
}

func TestGoalContinuation_MutationClearsSuppression(t *testing.T) {
	s := NewSession("m", "")
	if _, err := s.CreateGoal("g", 0); err != nil {
		t.Fatal(err)
	}
	s.GoalContinuation()
	s.GoalContinuation() // zero-progress → suppressed
	if _, ok := s.GoalContinuation(); ok {
		t.Fatal("precondition: suppressed")
	}

	if _, err := s.EditGoalObjective("revised"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.GoalContinuation(); !ok {
		t.Error("a goal mutation must clear the zero-progress suppression")
	}
}

func TestGoalBudgetSteer_InjectedOnceMidTurn(t *testing.T) {
	// Crossing the budget mid-turn stages the wrap-up steer; the loop drains
	// it into history ahead of the next LLM call, exactly once.
	s := NewSession("m", "")
	if _, err := s.CreateGoal("g", 100); err != nil {
		t.Fatal(err)
	}

	echo := ToolDefinition{Name: "echo", Description: "echo", Parameters: map[string]any{"type": "object"}}
	send := &fakeToolSender{replies: []Reply{
		{StopReason: "tool_use", InputTokens: 80, OutputTokens: 40,
			Blocks: []ContentBlock{NewToolUseBlock("t1", "echo", map[string]any{})}},
		{Content: "wrapping up", StopReason: "end_turn", InputTokens: 10, OutputTokens: 5},
	}}
	a := New(send, "claude-test")
	a.GoalAcct = s

	if _, err := a.RunStream(context.Background(), "go", []ToolDefinition{echo}, &fakeExecutor{}, nil); err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	// The second LLM call must have seen the injected budget steer.
	var steer int
	for _, m := range send.gotMsgs {
		text := m.Content
		if text == "" {
			text = textFromBlocks(m.Blocks)
		}
		if m.Role == RoleUser && strings.Contains(text, "<goal_context>") &&
			strings.Contains(strings.ToLower(text), "wrap up this turn soon") {
			steer++
		}
	}
	if steer != 1 {
		t.Errorf("budget steer should appear exactly once in the final call's history, got %d", steer)
	}

	// The staged steer was consumed — nothing left over.
	if leftover, ok := s.ConsumeGoalBudgetSteer(); ok {
		t.Errorf("steer must be consumed by the loop, leftover %q", leftover)
	}
	if a.Inbox.HasPending() {
		t.Error("no steer should remain queued after the turn")
	}
}

func TestGoalCreatedMidTurn_SkipsThatTicksTokens(t *testing.T) {
	s := NewSession("m", "")
	// Mid-turn creation: no turn-start reset between CreateGoal and the next
	// accounting tick — its token delta belongs to pre-goal work.
	if _, err := s.CreateGoal("g", 0); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.AccountGoalUsage(5000); g.TokensUsed != 0 {
		t.Errorf("creating tick must not be billed, got %d", g.TokensUsed)
	}
	if g, _ := s.AccountGoalUsage(70); g.TokensUsed != 70 {
		t.Errorf("subsequent ticks bill normally, got %d", g.TokensUsed)
	}
}

func TestStripSystemReminders_StripsGoalContext(t *testing.T) {
	in := "before <goal_context>\nhidden steering\n</goal_context> after"
	if got := StripSystemReminders(in); got != "before  after" {
		t.Errorf("goal_context not stripped: %q", got)
	}
	mixed := "<system-reminder>note</system-reminder><goal_context>steer</goal_context>"
	if got := strings.TrimSpace(StripSystemReminders(mixed)); got != "" {
		t.Errorf("pure injected content should strip to empty, got %q", got)
	}
}

func TestIsRateLimitErr(t *testing.T) {
	for _, err := range []error{
		errors.New("anthropic: HTTP 429: rate limited"),
		errors.New("openai: HTTP 429 (insufficient_quota): You exceeded your current quota"),
		errors.New("agent: loop[3]: send: Rate limit reached for requests"),
		errors.New("gateway: too many requests"),
	} {
		if !IsRateLimitErr(err) {
			t.Errorf("should classify as rate limit: %v", err)
		}
	}
	for _, err := range []error{
		nil,
		errors.New("anthropic: HTTP 500: overloaded"),
		errors.New("context deadline exceeded"),
	} {
		if IsRateLimitErr(err) {
			t.Errorf("should NOT classify as rate limit: %v", err)
		}
	}
}
