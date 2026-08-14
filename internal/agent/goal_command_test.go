package agent

import (
	"strings"
	"testing"
)

func TestGoalCommand_Grammar(t *testing.T) {
	s := NewSession("m", "")

	// Bare with no goal → usage.
	if r, _ := GoalCommand(s, ""); !strings.Contains(r, "No goal is currently set") {
		t.Errorf("bare/no-goal reply = %q", r)
	}

	// Create.
	if r, start := GoalCommand(s, "ship the release"); !strings.Contains(r, "Goal set") || start != GoalStartFresh {
		t.Errorf("create reply = %q, start = %v", r, start)
	}
	if g, ok := s.GoalSnapshot(); !ok || g.Objective != "ship the release" || g.Status != GoalActive {
		t.Fatalf("create failed: %+v", g)
	}

	// Bare with a goal → summary with command hints.
	r, start := GoalCommand(s, "")
	if !strings.Contains(r, "ship the release") || !strings.Contains(r, "active") || !strings.Contains(r, "/goal pause") {
		t.Errorf("summary reply = %q", r)
	}
	if start != GoalStartNone {
		t.Error("reading the goal must not start work")
	}

	// New objective over an unfinished goal is refused with the replace hint.
	if r, start := GoalCommand(s, "something else"); !strings.Contains(r, "/goal replace") || start != GoalStartNone {
		t.Errorf("refusal reply = %q, start = %v", r, start)
	}
	if g, _ := s.GoalSnapshot(); g.Objective != "ship the release" {
		t.Error("unfinished goal must not be silently replaced")
	}

	// Edit keeps counters, and hands the next turn a steer rather than
	// starting one of its own.
	s.ResetGoalWallClock()
	s.AccountGoalUsage(42)
	if r, start := GoalCommand(s, "edit revised objective"); !strings.Contains(r, "Goal updated") || start != GoalStartNone {
		t.Errorf("edit reply = %q, start = %v", r, start)
	}
	if g, _ := s.GoalSnapshot(); g.Objective != "revised objective" || g.TokensUsed != 42 {
		t.Errorf("edit lost state: %+v", g)
	}

	// Bare edit/replace print usage without touching the goal.
	if r, start := GoalCommand(s, "edit"); !strings.Contains(r, "Usage: /goal edit") || start != GoalStartNone {
		t.Errorf("bare edit reply = %q, start = %v", r, start)
	}
	if r, start := GoalCommand(s, "replace"); !strings.Contains(r, "Usage: /goal replace") || start != GoalStartNone {
		t.Errorf("bare replace reply = %q, start = %v", r, start)
	}

	// Pause / resume. Only resume returns to pursuing the goal.
	if r, start := GoalCommand(s, "pause"); !strings.Contains(r, "paused") || start != GoalStartNone {
		t.Errorf("pause reply = %q, start = %v", r, start)
	}
	if r, start := GoalCommand(s, "resume"); !strings.Contains(r, "active") || start != GoalStartResumed {
		t.Errorf("resume reply = %q, start = %v", r, start)
	}

	// Explicit replace mints a fresh goal.
	old, _ := s.GoalSnapshot()
	if r, start := GoalCommand(s, "replace brand new goal"); !strings.Contains(r, "Goal replaced") || start != GoalStartFresh {
		t.Errorf("replace reply = %q, start = %v", r, start)
	}
	if g, _ := s.GoalSnapshot(); g.ID == old.ID || g.TokensUsed != 0 {
		t.Errorf("replace not fresh: %+v", g)
	}

	// A complete goal is replaced without ceremony.
	if _, err := s.SetGoalStatus(GoalComplete); err != nil {
		t.Fatal(err)
	}
	if r, start := GoalCommand(s, "next objective"); !strings.Contains(r, "Goal set") || start != GoalStartFresh {
		t.Errorf("complete-replace reply = %q, start = %v", r, start)
	}

	// Clear.
	if r, start := GoalCommand(s, "clear"); r != "Goal cleared" || start != GoalStartNone {
		t.Errorf("clear reply = %q, start = %v", r, start)
	}
	if r, start := GoalCommand(s, "clear"); r != "No goal to clear" || start != GoalStartNone {
		t.Errorf("re-clear reply = %q, start = %v", r, start)
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

func TestCacheUtilizationPct(t *testing.T) {
	cases := []struct {
		name                    string
		input, read, write, pct int
		ok                      bool
	}{
		{"no cache info at all", 1000, 0, 0, 0, false},
		{"zero everything", 0, 0, 0, 0, false},
		{"warming turn (write only)", 100, 0, 2000, 0, true},
		{"typical hit", 114, 2304, 0, 95, true},
		{"hit plus write", 100, 800, 100, 80, true},
		{"fully cached", 0, 500, 0, 100, true},
	}
	for _, c := range cases {
		pct, ok := CacheUtilizationPct(c.input, c.read, c.write)
		if pct != c.pct || ok != c.ok {
			t.Errorf("%s: CacheUtilizationPct(%d, %d, %d) = (%d, %v), want (%d, %v)",
				c.name, c.input, c.read, c.write, pct, ok, c.pct, c.ok)
		}
	}
}
