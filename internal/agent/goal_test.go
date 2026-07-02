package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestValidateGoalObjective(t *testing.T) {
	if err := ValidateGoalObjective(""); err == nil {
		t.Error("empty objective should fail validation")
	}
	if err := ValidateGoalObjective(strings.Repeat("字", MaxGoalObjectiveChars+1)); err == nil {
		t.Error("over-long objective should fail validation")
	}
	if err := ValidateGoalObjective(strings.Repeat("字", MaxGoalObjectiveChars)); err != nil {
		t.Errorf("objective at the rune limit should pass, got %v", err)
	}
}

func TestCreateGoal(t *testing.T) {
	s := NewSession("m", "")
	g, err := s.CreateGoal("  ship the release notes  ", 0)
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if g.Status != GoalActive {
		t.Errorf("status = %q, want active", g.Status)
	}
	if g.Objective != "ship the release notes" {
		t.Errorf("objective not trimmed: %q", g.Objective)
	}
	if g.ID == "" || g.TokensUsed != 0 || g.TimeUsedSeconds != 0 {
		t.Errorf("fresh goal has unexpected fields: %+v", g)
	}
	if s.Title != "ship the release notes" {
		t.Errorf("empty title should be seeded from objective, got %q", s.Title)
	}

	// A second create fails regardless of status — even complete.
	if _, err := s.CreateGoal("another", 0); err == nil {
		t.Error("CreateGoal over an existing goal should fail")
	}
	if _, err := s.SetGoalStatus(GoalComplete); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateGoal("another", 0); err == nil {
		t.Error("CreateGoal over a complete goal should still fail")
	}
}

func TestCreateGoal_RejectsBadInput(t *testing.T) {
	s := NewSession("m", "")
	if _, err := s.CreateGoal("   ", 0); err == nil {
		t.Error("whitespace objective should fail")
	}
	if _, err := s.CreateGoal("ok", -1); err == nil {
		t.Error("negative budget should fail")
	}
	if _, ok := s.GoalSnapshot(); ok {
		t.Error("failed create must not leave a goal behind")
	}
}

func TestReplaceGoal_MintsNewIDAndFreshCounters(t *testing.T) {
	s := NewSession("m", "")
	old, err := s.CreateGoal("first", 0)
	if err != nil {
		t.Fatal(err)
	}
	s.Goal.TokensUsed = 500

	g, err := s.ReplaceGoal("second", 100)
	if err != nil {
		t.Fatalf("ReplaceGoal: %v", err)
	}
	if g.ID == old.ID {
		t.Error("replacement should mint a new goal ID")
	}
	if g.TokensUsed != 0 || g.Status != GoalActive || g.TokenBudget != 100 {
		t.Errorf("replacement not fresh: %+v", g)
	}
}

func TestEditGoalObjective(t *testing.T) {
	s := NewSession("m", "")
	if _, err := s.EditGoalObjective("x"); err == nil {
		t.Error("edit with no goal should fail")
	}
	if _, err := s.CreateGoal("first", 200); err != nil {
		t.Fatal(err)
	}
	s.Goal.TokensUsed = 150

	g, err := s.EditGoalObjective("revised")
	if err != nil {
		t.Fatal(err)
	}
	if g.Objective != "revised" || g.TokensUsed != 150 || g.TokenBudget != 200 {
		t.Errorf("edit should keep counters and budget: %+v", g)
	}

	// A paused goal stays paused on edit.
	if _, err := s.SetGoalStatus(GoalPaused); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.EditGoalObjective("still paused"); g.Status != GoalPaused {
		t.Errorf("paused goal should stay paused on edit, got %q", g.Status)
	}

	// A complete goal re-activates on edit.
	if _, err := s.SetGoalStatus(GoalComplete); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.EditGoalObjective("more work"); g.Status != GoalActive {
		t.Errorf("complete goal should re-activate on edit, got %q", g.Status)
	}

	// A budget_limited goal that is still over budget re-activates straight
	// back into budget_limited.
	s.Goal.TokensUsed = 250
	if _, err := s.SetGoalStatus(GoalBudgetLimited); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.EditGoalObjective("over budget"); g.Status != GoalBudgetLimited {
		t.Errorf("over-budget edit should land on budget_limited, got %q", g.Status)
	}
}

func TestSetGoalStatus(t *testing.T) {
	s := NewSession("m", "")
	if _, err := s.SetGoalStatus(GoalPaused); err == nil {
		t.Error("status change with no goal should fail")
	}
	if _, err := s.CreateGoal("g", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetGoalStatus(GoalStatus("bogus")); err == nil {
		t.Error("unknown status should fail")
	}

	g, err := s.SetGoalStatus(GoalPaused)
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != GoalPaused {
		t.Errorf("status = %q, want paused", g.Status)
	}
	if !s.goalWallClockAt.IsZero() {
		t.Error("pausing should stop the wall clock")
	}

	if g, _ = s.SetGoalStatus(GoalActive); g.Status != GoalActive {
		t.Errorf("resume: status = %q, want active", g.Status)
	}
	if s.goalWallClockAt.IsZero() {
		t.Error("resuming should restart the wall clock")
	}
}

func TestSetGoalStatus_ActivatingOverBudgetGoalStaysBudgetLimited(t *testing.T) {
	s := NewSession("m", "")
	if _, err := s.CreateGoal("g", 100); err != nil {
		t.Fatal(err)
	}
	s.Goal.TokensUsed = 100
	if _, err := s.SetGoalStatus(GoalPaused); err != nil {
		t.Fatal(err)
	}
	g, err := s.SetGoalStatus(GoalActive)
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != GoalBudgetLimited {
		t.Errorf("activating an over-budget goal should land on budget_limited, got %q", g.Status)
	}
}

func TestClearGoal(t *testing.T) {
	s := NewSession("m", "")
	if s.ClearGoal() {
		t.Error("clearing with no goal should report false")
	}
	if _, err := s.CreateGoal("g", 0); err != nil {
		t.Fatal(err)
	}
	if !s.ClearGoal() {
		t.Error("clearing an existing goal should report true")
	}
	if _, ok := s.GoalSnapshot(); ok {
		t.Error("goal should be gone after clear")
	}
}

func TestAccountGoalUsage(t *testing.T) {
	s := NewSession("m", "")

	// No goal: nothing to account.
	if _, changed := s.AccountGoalUsage(100); changed {
		t.Error("accounting with no goal should report unchanged")
	}

	if _, err := s.CreateGoal("g", 1000); err != nil {
		t.Fatal(err)
	}
	g, changed := s.AccountGoalUsage(400)
	if !changed || g.TokensUsed != 400 || g.Status != GoalActive {
		t.Errorf("after 400: changed=%v goal=%+v", changed, g)
	}

	// Crossing the budget flips active → budget_limited (>= semantics).
	g, _ = s.AccountGoalUsage(600)
	if g.TokensUsed != 1000 || g.Status != GoalBudgetLimited {
		t.Errorf("crossing budget: goal=%+v", g)
	}

	// budget_limited keeps accruing in-flight usage but never un-flips.
	g, changed = s.AccountGoalUsage(50)
	if !changed || g.TokensUsed != 1050 || g.Status != GoalBudgetLimited {
		t.Errorf("post-limit accrual: changed=%v goal=%+v", changed, g)
	}

	// Paused goals accrue nothing.
	if _, err := s.SetGoalStatus(GoalPaused); err != nil {
		t.Fatal(err)
	}
	if _, changed := s.AccountGoalUsage(100); changed {
		t.Error("paused goal should not accrue usage")
	}
}

func TestAccountGoalUsage_WallClock(t *testing.T) {
	s := NewSession("m", "")
	if _, err := s.CreateGoal("g", 0); err != nil {
		t.Fatal(err)
	}
	s.goalWallClockAt = time.Now().Add(-3500 * time.Millisecond)

	g, changed := s.AccountGoalUsage(0)
	if !changed || g.TimeUsedSeconds != 3 {
		t.Errorf("wall clock: changed=%v seconds=%d, want 3", changed, g.TimeUsedSeconds)
	}
	// The sub-second remainder carries over: the baseline advanced by exactly
	// 3s, so ~500ms remain banked toward the next accounting.
	if since := time.Since(s.goalWallClockAt); since < 400*time.Millisecond || since > 2*time.Second {
		t.Errorf("baseline should advance by whole accounted seconds, remainder=%v", since)
	}

	// Zero tokens + zero elapsed seconds: unchanged.
	if _, changed := s.AccountGoalUsage(0); changed {
		t.Error("no delta should report unchanged")
	}
}

func TestSetGoalStatus_AccountsWallClockTail(t *testing.T) {
	s := NewSession("m", "")
	if _, err := s.CreateGoal("g", 0); err != nil {
		t.Fatal(err)
	}
	s.goalWallClockAt = time.Now().Add(-5 * time.Second)
	g, err := s.SetGoalStatus(GoalPaused)
	if err != nil {
		t.Fatal(err)
	}
	if g.TimeUsedSeconds < 5 {
		t.Errorf("pausing should bank the in-flight wall clock, got %ds", g.TimeUsedSeconds)
	}
}

func TestGoal_PersistsAcrossSaveAndLoad(t *testing.T) {
	// Goal created before the first Save rides the meta header.
	setTempHome(t)

	s := NewSession("m", "")
	if _, err := s.CreateGoal("persisted objective", 500); err != nil {
		t.Fatal(err)
	}
	s.Messages = []Message{NewUserMessage("ping")}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := LoadSession(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	g, ok := got.GoalSnapshot()
	if !ok {
		t.Fatal("goal missing after reload")
	}
	if g.Objective != "persisted objective" || g.TokenBudget != 500 || g.Status != GoalActive {
		t.Errorf("reloaded goal = %+v", g)
	}
	if got.goalWallClockAt.IsZero() {
		t.Error("loading an active goal should restart the wall-clock baseline")
	}
}

func TestGoal_AppendRecordAfterFirstSave(t *testing.T) {
	// Mutations after the transcript exists append "goal" records; the last
	// one wins on load, and a clear round-trips as goal-gone.
	setTempHome(t)

	s := NewSession("m", "")
	s.Messages = []Message{NewUserMessage("ping")}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	if _, err := s.CreateGoal("first", 0); err != nil {
		t.Fatal(err)
	}
	s.AccountGoalUsage(42)
	if _, err := s.EditGoalObjective("second"); err != nil {
		t.Fatal(err)
	}

	got, err := LoadSession(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	g, ok := got.GoalSnapshot()
	if !ok {
		t.Fatal("goal missing after reload")
	}
	if g.Objective != "second" || g.TokensUsed != 42 {
		t.Errorf("last goal record should win: %+v", g)
	}

	s.ClearGoal()
	got, err = LoadSession(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.GoalSnapshot(); ok {
		t.Error("cleared goal should stay cleared after reload")
	}

	// A paused goal must not restart the wall clock on load.
	if _, err := s.CreateGoal("third", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetGoalStatus(GoalPaused); err != nil {
		t.Fatal(err)
	}
	got, err = LoadSession(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.goalWallClockAt.IsZero() {
		t.Error("loading a paused goal must not start the wall clock")
	}
}

func TestAgent_AccountsGoalUsagePerReply(t *testing.T) {
	// The turn loop bills non-cached input + output to the session's goal and
	// emits EventGoalUpdated; cache reads are free.
	s := NewSession("m", "")
	if _, err := s.CreateGoal("g", 0); err != nil {
		t.Fatal(err)
	}

	send := &fakeToolSender{replies: []Reply{
		{Content: "done", StopReason: "end_turn", InputTokens: 300, OutputTokens: 70, CacheReadTokens: 5000},
	}}
	a := New(send, "claude-test")
	a.GoalAcct = s

	var goalEvents []Goal
	_, err := a.RunStream(context.Background(), "go", nil, nil, func(ev AgentEvent) {
		if ev.Kind == EventGoalUpdated && ev.Goal != nil {
			goalEvents = append(goalEvents, *ev.Goal)
		}
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	g, _ := s.GoalSnapshot()
	if g.TokensUsed != 370 {
		t.Errorf("TokensUsed = %d, want 370 (cache reads must not be billed)", g.TokensUsed)
	}
	if len(goalEvents) == 0 {
		t.Fatal("expected at least one EventGoalUpdated")
	}
	if last := goalEvents[len(goalEvents)-1]; last.TokensUsed != 370 {
		t.Errorf("event TokensUsed = %d, want 370", last.TokensUsed)
	}
}

func TestAgent_GoalBudgetCrossingFlipsMidTurn(t *testing.T) {
	// A multi-round turn crosses the budget on round 1; the flip is visible
	// in the emitted events before the turn ends.
	s := NewSession("m", "")
	if _, err := s.CreateGoal("g", 100); err != nil {
		t.Fatal(err)
	}

	echo := ToolDefinition{Name: "echo", Description: "echo", Parameters: map[string]any{"type": "object"}}
	send := &fakeToolSender{replies: []Reply{
		{StopReason: "tool_use", InputTokens: 80, OutputTokens: 40,
			Blocks: []ContentBlock{NewToolUseBlock("t1", "echo", map[string]any{})}},
		{Content: "done", StopReason: "end_turn", InputTokens: 10, OutputTokens: 5},
	}}
	a := New(send, "claude-test")
	a.GoalAcct = s

	var statuses []GoalStatus
	_, err := a.RunStream(context.Background(), "go", []ToolDefinition{echo}, &fakeExecutor{}, func(ev AgentEvent) {
		if ev.Kind == EventGoalUpdated && ev.Goal != nil {
			statuses = append(statuses, ev.Goal.Status)
		}
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	if len(statuses) == 0 || statuses[0] != GoalBudgetLimited {
		t.Errorf("first accounting should flip to budget_limited, got %v", statuses)
	}
	g, _ := s.GoalSnapshot()
	if g.TokensUsed != 135 || g.Status != GoalBudgetLimited {
		t.Errorf("final goal = %+v", g)
	}
}

func TestAgent_NoGoalAccountantIsANoOp(t *testing.T) {
	send := &fakeSender{reply: Reply{Content: "hi", StopReason: "end_turn", InputTokens: 10, OutputTokens: 5}}
	a := New(send, "claude-test")
	if _, err := a.Turn(context.Background(), "hello"); err != nil {
		t.Fatalf("Turn: %v", err)
	}
}
