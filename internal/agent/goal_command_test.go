package agent

import (
	"strings"
	"testing"
)

func TestGoalCommand_Grammar(t *testing.T) {
	s := NewSession("m", "")

	// Bare with no goal → usage.
	if r := GoalCommand(s, ""); !strings.Contains(r, "No goal is currently set") {
		t.Errorf("bare/no-goal reply = %q", r)
	}

	// Create.
	if r := GoalCommand(s, "ship the release"); !strings.Contains(r, "Goal set") {
		t.Errorf("create reply = %q", r)
	}
	if g, ok := s.GoalSnapshot(); !ok || g.Objective != "ship the release" || g.Status != GoalActive {
		t.Fatalf("create failed: %+v", g)
	}

	// Bare with a goal → summary with command hints.
	r := GoalCommand(s, "")
	if !strings.Contains(r, "ship the release") || !strings.Contains(r, "active") || !strings.Contains(r, "/goal pause") {
		t.Errorf("summary reply = %q", r)
	}

	// New objective over an unfinished goal is refused with the replace hint.
	if r := GoalCommand(s, "something else"); !strings.Contains(r, "/goal replace") {
		t.Errorf("refusal reply = %q", r)
	}
	if g, _ := s.GoalSnapshot(); g.Objective != "ship the release" {
		t.Error("unfinished goal must not be silently replaced")
	}

	// Edit keeps counters.
	s.ResetGoalWallClock()
	s.AccountGoalUsage(42)
	if r := GoalCommand(s, "edit revised objective"); !strings.Contains(r, "Goal updated") {
		t.Errorf("edit reply = %q", r)
	}
	if g, _ := s.GoalSnapshot(); g.Objective != "revised objective" || g.TokensUsed != 42 {
		t.Errorf("edit lost state: %+v", g)
	}

	// Bare edit/replace print usage without touching the goal.
	if r := GoalCommand(s, "edit"); !strings.Contains(r, "Usage: /goal edit") {
		t.Errorf("bare edit reply = %q", r)
	}
	if r := GoalCommand(s, "replace"); !strings.Contains(r, "Usage: /goal replace") {
		t.Errorf("bare replace reply = %q", r)
	}

	// Pause / resume.
	if r := GoalCommand(s, "pause"); !strings.Contains(r, "paused") {
		t.Errorf("pause reply = %q", r)
	}
	if r := GoalCommand(s, "resume"); !strings.Contains(r, "active") {
		t.Errorf("resume reply = %q", r)
	}

	// Explicit replace mints a fresh goal.
	old, _ := s.GoalSnapshot()
	if r := GoalCommand(s, "replace brand new goal"); !strings.Contains(r, "Goal replaced") {
		t.Errorf("replace reply = %q", r)
	}
	if g, _ := s.GoalSnapshot(); g.ID == old.ID || g.TokensUsed != 0 {
		t.Errorf("replace not fresh: %+v", g)
	}

	// A complete goal is replaced without ceremony.
	if _, err := s.SetGoalStatus(GoalComplete); err != nil {
		t.Fatal(err)
	}
	if r := GoalCommand(s, "next objective"); !strings.Contains(r, "Goal set") {
		t.Errorf("complete-replace reply = %q", r)
	}

	// Clear.
	if r := GoalCommand(s, "clear"); r != "Goal cleared" {
		t.Errorf("clear reply = %q", r)
	}
	if r := GoalCommand(s, "clear"); r != "No goal to clear" {
		t.Errorf("re-clear reply = %q", r)
	}
}

func TestFormatGoalHelpers(t *testing.T) {
	for n, want := range map[int64]string{950: "950", 1200: "1.2K", 50000: "50K", 1_500_000: "1.5M"} {
		if got := FormatGoalTokens(n); got != want {
			t.Errorf("FormatGoalTokens(%d) = %q, want %q", n, got, want)
		}
	}
	for sec, want := range map[int64]string{45: "45s", 720: "12m", 5400: "1h 30m", 183900: "2d 3h 5m"} {
		if got := FormatGoalElapsed(sec); got != want {
			t.Errorf("FormatGoalElapsed(%d) = %q, want %q", sec, got, want)
		}
	}
	g := Goal{TimeUsedSeconds: 120, TokensUsed: 63900, TokenBudget: 50000}
	if got := GoalUsageLine(g); got != "2m, 63.9K/50K tokens" {
		t.Errorf("GoalUsageLine = %q", got)
	}
}
