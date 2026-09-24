package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/hooks"
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
		got := in.Reminder(input, nil)
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

	off := in.Reminder("just say hi", nil)
	if strings.Contains(off, "deploy via Lark bot") {
		t.Errorf("untriggered rule leaked:\n%s", off)
	}

	on := in.Reminder("帮我部署到 311", nil)
	if !strings.Contains(on, "deploy via Lark bot") {
		t.Errorf("triggered rule missing:\n%s", on)
	}
}

func TestReminder_TriggeredDedupPerSession(t *testing.T) {
	in := newTestInjector()

	first := in.Reminder("deploy now", nil)
	if !strings.Contains(first, "deploy via Lark bot") {
		t.Fatalf("first deploy turn should surface the rule:\n%s", first)
	}
	second := in.Reminder("deploy again", nil)
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
	if got := in.Reminder("unrelated input", nil); got != "" {
		t.Errorf("expected empty reminder, got:\n%s", got)
	}
}

func TestReminder_NilSafe(t *testing.T) {
	var in *Injector
	if got := in.Reminder("anything", nil); got != "" {
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
	in.Reminder("next user turn", nil) // new turn re-arms the latch
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

// ─── Restating the always-apply rules ───────────────────────────────────────

// history builds user texts where the always rules were restated `ago` user
// messages before the end (ago < 0: never).
func historyWithRestatement(in *Injector, ago int) []string {
	var h []string
	first := in.Reminder("start", nil) // full restatement, as a first turn gets
	if ago >= 0 {
		h = append(h, first+"\n\nstart")
	}
	for i := 0; i < ago; i++ {
		h = append(h, "plain message")
	}
	if ago < 0 {
		h = append(h, "plain message", "another")
	}
	return h
}

func TestReminder_AlwaysRestatedOnlyEveryN(t *testing.T) {
	cases := []struct {
		name      string
		ago       int
		wantRules bool
		wantFull  bool
	}{
		{"never restated (or compacted away)", -1, true, true},
		{"restated just now", 0, false, false},
		{"restated a few turns ago", restateEvery - 1, false, false},
		{"restated restateEvery turns ago", restateEvery, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := newTestInjector()
			h := historyWithRestatement(in, c.ago)
			got := in.Reminder("next", h)
			if hasRule := strings.Contains(got, "never commit on main"); hasRule != c.wantRules {
				t.Fatalf("always rule present = %v, want %v:\n%s", hasRule, c.wantRules, got)
			}
			if !c.wantRules {
				if got != "" {
					t.Errorf("nothing to surface, got:\n%s", got)
				}
				return
			}
			if full := strings.Contains(got, fullHeader); full != c.wantFull {
				t.Errorf("full header = %v, want %v:\n%s", full, c.wantFull, got)
			}
		})
	}
}

// A newly triggered rule always comes with the full header, and doesn't drag
// the always rules along when they aren't due.
func TestReminder_TriggeredCarriesFullHeaderWithoutAlways(t *testing.T) {
	in := newTestInjector()
	h := historyWithRestatement(in, 1)
	got := in.Reminder("帮我部署", h)
	if !strings.Contains(got, "deploy via Lark bot") || !strings.Contains(got, fullHeader) {
		t.Fatalf("triggered rule with full header expected:\n%s", got)
	}
	if strings.Contains(got, "never commit on main") {
		t.Errorf("always rule restated although not due:\n%s", got)
	}
}

// The hook reads the history when it fires.
func TestInjector_HookReadsHistoryAtFireTime(t *testing.T) {
	in := newTestInjector()
	e := hooks.NewEngine(nil)
	var history []string
	in.RegisterHooks(e, func() []string { return history })
	submit := func() string {
		return e.Inject(context.Background(), hooks.Payload{Event: hooks.EventUserPromptSubmit, UserInput: "hi"})
	}

	first := submit()
	if !strings.Contains(first, "never commit on main") {
		t.Fatalf("first turn must restate:\n%s", first)
	}
	history = append(history, first+"\n\nhi")
	if got := submit(); got != "" {
		t.Errorf("second turn with the restatement in history: got %q", got)
	}
}
