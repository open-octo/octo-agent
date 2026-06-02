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
