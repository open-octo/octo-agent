package memory

import (
	"strings"
	"testing"
)

func newTestInjector() *Injector {
	return NewInjector(&Rules{
		Always: []Rule{{Text: "never commit on main"}},
		Triggered: []Rule{
			{Text: "deploy via Lark bot", Triggers: []string{"deploy", "部署"}},
			{Text: "design docs are current-state only", Triggers: []string{"设计文档"}},
		},
	})
}

func TestReminder_AlwaysEveryTurn(t *testing.T) {
	in := newTestInjector()
	for _, input := range []string{"hello", "what's up", "fix this bug"} {
		got := in.Reminder(input)
		if !strings.Contains(got, "never commit on main") {
			t.Errorf("input %q: always rule missing from reminder:\n%s", input, got)
		}
		if !strings.Contains(got, "<system-reminder>") {
			t.Errorf("input %q: reminder not wrapped:\n%s", input, got)
		}
	}
}

func TestReminder_TriggeredOnlyOnMatch(t *testing.T) {
	in := newTestInjector()

	off := in.Reminder("just say hi")
	if strings.Contains(off, "deploy via Lark bot") {
		t.Errorf("untriggered rule leaked:\n%s", off)
	}

	on := in.Reminder("帮我部署到 311")
	if !strings.Contains(on, "deploy via Lark bot") {
		t.Errorf("triggered rule missing:\n%s", on)
	}
}

func TestReminder_TriggeredDedupPerSession(t *testing.T) {
	in := newTestInjector()

	first := in.Reminder("deploy now")
	if !strings.Contains(first, "deploy via Lark bot") {
		t.Fatalf("first deploy turn should surface the rule:\n%s", first)
	}
	second := in.Reminder("deploy again")
	if strings.Contains(second, "deploy via Lark bot") {
		t.Errorf("rule should not repeat in same session:\n%s", second)
	}
	// Always block still present on the second turn.
	if !strings.Contains(second, "never commit on main") {
		t.Errorf("always block dropped on second turn:\n%s", second)
	}
}

func TestReminder_EmptyWhenNothing(t *testing.T) {
	in := NewInjector(&Rules{
		Triggered: []Rule{{Text: "x", Triggers: []string{"deploy"}}},
	})
	if got := in.Reminder("unrelated input"); got != "" {
		t.Errorf("expected empty reminder, got:\n%s", got)
	}
}

func TestReminder_NilSafe(t *testing.T) {
	var in *Injector
	if got := in.Reminder("anything"); got != "" {
		t.Errorf("nil injector should return empty, got %q", got)
	}
}

// ─── Save-nudge ─────────────────────────────────────────────────────────────

func term(cmd string) map[string]any { return map[string]any{"command": cmd} }

func TestSaveNudge_FiresOnMilestoneCommands(t *testing.T) {
	for _, cmd := range []string{
		"gh pr create --title x",
		"gh pr merge 42 --squash",
		"cd /repo && gh pr merge",
	} {
		in := NewInjector(nil)
		got := in.SaveNudge("terminal", term(cmd))
		if !strings.Contains(got, "<system-reminder>") {
			t.Errorf("command %q: expected nudge, got %q", cmd, got)
		}
	}
}

func TestSaveNudge_SilentOnEverythingElse(t *testing.T) {
	in := NewInjector(nil)
	cases := []struct {
		tool string
		in   map[string]any
	}{
		{"terminal", term("git status")},
		{"terminal", term("gh pr view 42")},
		{"terminal", term("gh pr list")},
		{"terminal", term("echo gh prX merge")},
		{"terminal", map[string]any{}}, // no command key
		{"write_file", term("gh pr merge")},
	}
	for _, c := range cases {
		if got := in.SaveNudge(c.tool, c.in); got != "" {
			t.Errorf("tool %s input %v: expected silence, got %q", c.tool, c.in, got)
		}
	}
}

func TestSaveNudge_OncePerTurn_RearmedByReminder(t *testing.T) {
	in := NewInjector(nil)
	if in.SaveNudge("terminal", term("gh pr create")) == "" {
		t.Fatal("first milestone should nudge")
	}
	if got := in.SaveNudge("terminal", term("gh pr merge 1")); got != "" {
		t.Errorf("second milestone in same turn should be silent, got %q", got)
	}
	in.Reminder("next user turn") // new turn re-arms the latch
	if in.SaveNudge("terminal", term("gh pr merge 2")) == "" {
		t.Error("milestone on a later turn should nudge again")
	}
}

func TestSaveNudge_NilSafe(t *testing.T) {
	var in *Injector
	if got := in.SaveNudge("terminal", term("gh pr merge")); got != "" {
		t.Errorf("nil injector should return empty, got %q", got)
	}
}
