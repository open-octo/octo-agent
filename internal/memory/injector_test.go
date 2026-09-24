package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
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

func TestReminder_AlwaysEveryTurnWhenHistoryUnknown(t *testing.T) {
	in := newTestInjector()
	for _, input := range []string{"hello", "what's up", "fix this bug"} {
		got := in.Reminder(input, HistoryView{})
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

	off := in.Reminder("just say hi", HistoryView{})
	if strings.Contains(off, "deploy via Lark bot") {
		t.Errorf("untriggered rule leaked:\n%s", off)
	}

	on := in.Reminder("帮我部署到 311", HistoryView{})
	if !strings.Contains(on, "deploy via Lark bot") {
		t.Errorf("triggered rule missing:\n%s", on)
	}
}

func TestReminder_TriggeredDedupPerSession(t *testing.T) {
	in := newTestInjector()

	first := in.Reminder("deploy now", HistoryView{})
	if !strings.Contains(first, "deploy via Lark bot") {
		t.Fatalf("first deploy turn should surface the rule:\n%s", first)
	}
	second := in.Reminder("deploy again", HistoryView{})
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
	if got := in.Reminder("unrelated input", HistoryView{}); got != "" {
		t.Errorf("expected empty reminder, got:\n%s", got)
	}
}

func TestReminder_NilSafe(t *testing.T) {
	var in *Injector
	if got := in.Reminder("anything", HistoryView{}); got != "" {
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
	in.Reminder("next user turn", HistoryView{}) // new turn re-arms the latch
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

// view fakes what the model sees: tokens since the last restatement (or the
// start), whether there was one, and the system prompt.
func view(since int, restated bool, system string) HistoryView {
	return HistoryView{
		TokensSince: func(func(string) bool) (int, bool) { return since, restated },
		System:      func() string { return system },
	}
}

const promptWithRules = "…memory block… never commit on main …"

func TestReminder_AlwaysRestatedByDistance(t *testing.T) {
	cases := []struct {
		name string
		v    HistoryView
		want bool
	}{
		{"fresh session, rules in the prompt", view(0, false, promptWithRules), false},
		{"conversation still short", view(restateAfterTokens-1, false, promptWithRules), false},
		{"conversation grown past the threshold", view(restateAfterTokens, false, promptWithRules), true},
		{"restated recently", view(10, true, promptWithRules), false},
		{"grown past the threshold since the restatement", view(restateAfterTokens, true, promptWithRules), true},
		{"rule missing from a frozen prompt", view(0, false, "a prompt frozen before the rule was added"), true},
		{"missing rule already restated", view(10, true, "a prompt frozen before the rule was added"), false},
		{"history unknown", HistoryView{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := newTestInjector().Reminder("next", c.v)
			if has := strings.Contains(got, "never commit on main"); has != c.want {
				t.Fatalf("always rule present = %v, want %v:\n%s", has, c.want, got)
			}
			if !c.want && got != "" {
				t.Errorf("nothing to surface, got:\n%s", got)
			}
			if c.want && (strings.Contains(got, fullHeader) || !isRestatement(got)) {
				t.Errorf("a restatement alone should carry the short header and be recognisable:\n%s", got)
			}
		})
	}
}

// A newly triggered rule always comes with the full header, and doesn't drag
// the always rules along when they aren't due; when they are, both go out and
// the result still counts as a restatement.
func TestReminder_TriggeredWithFullHeader(t *testing.T) {
	notDue := newTestInjector().Reminder("帮我部署", view(10, true, promptWithRules))
	if !strings.Contains(notDue, "deploy via Lark bot") || !strings.Contains(notDue, fullHeader) {
		t.Fatalf("triggered rule with full header expected:\n%s", notDue)
	}
	if strings.Contains(notDue, "never commit on main") || isRestatement(notDue) {
		t.Errorf("always rule restated although not due:\n%s", notDue)
	}

	due := newTestInjector().Reminder("帮我部署", view(restateAfterTokens, true, promptWithRules))
	if !strings.Contains(due, "deploy via Lark bot") || !strings.Contains(due, "never commit on main") || !isRestatement(due) {
		t.Errorf("both tiers expected, recognisable as a restatement:\n%s", due)
	}
}

func TestIsRestatement(t *testing.T) {
	cases := map[string]bool{
		"<system-reminder>\n" + shortAlways + "- r\n</system-reminder>\n\nhi": true,
		// The format before this change, still found in older sessions.
		"<system-reminder>\nReminders from your project memory. …\n\nAlways apply:\n- r\n</system-reminder>": true,
		// A triggered rule that merely mentions the words.
		"<system-reminder>\n" + fullHeader + "\nRelevant to what you're about to do:\n- Always apply gofmt\n</system-reminder>": false,
		"Always apply (from your project memory): pasted by the user":                                                           false,
	}
	for text, want := range cases {
		if got := isRestatement(text); got != want {
			t.Errorf("isRestatement(%q) = %v, want %v", text, got, want)
		}
	}
}

// Through the hook, against a real agent history: nothing at the start (the
// prompt carries the rules), a restatement once the conversation has grown,
// then nothing again right after it.
func TestInjector_HookAgainstRealHistory(t *testing.T) {
	in := newTestInjector()
	e := hooks.NewEngine(nil)
	h := agent.NewHistory()
	in.RegisterHooks(e, HistoryView{
		TokensSince: func(m func(string) bool) (int, bool) { return h.TokensSince(m) },
		System:      func() string { return promptWithRules },
	})
	submit := func() string {
		return e.Inject(context.Background(), hooks.Payload{Event: hooks.EventUserPromptSubmit, UserInput: "hi"})
	}

	if got := submit(); got != "" {
		t.Fatalf("first turn: the system prompt already carries the rules, got %q", got)
	}
	h.Append(agent.NewUserMessage("hi"))
	h.Append(agent.NewAssistantMessage(strings.Repeat("a long tool-heavy stretch of work ", restateAfterTokens/4)))
	got := submit()
	if !strings.Contains(got, "never commit on main") {
		t.Fatalf("the conversation has grown past the threshold:\n%s", got)
	}
	h.Append(agent.NewUserMessage(got + "\n\nhi"))
	if got := submit(); got != "" {
		t.Errorf("just restated: got %q", got)
	}
}

// The premise behind skipping the first restatement: every always-apply rule
// the injector parses appears verbatim in the memory block the system prompt
// carries. Pinned against the real parse and render, so a parser that starts
// normalising rule text breaks this test rather than silently restating
// every rule on every session's first turn.
func TestReminder_RulesFromTheRealMemoryBlock(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, IndexFile), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("# Memory\n\n## 必须遵守\n- **Never** commit on `main`\n-   reply in Chinese  \n\n## 触发提醒\n- (触发: deploy) deploy via the bot\n")
	system := RenderInjection(dir)

	in := NewInjector(ParseRules(dir))
	if got := in.Reminder("hi", view(0, false, system)); got != "" {
		t.Fatalf("rules are in the system prompt, yet the first turn restated them:\n%s", got)
	}

	// A rule added after the prompt froze reaches the injector but not the
	// prompt: it is restated at once.
	write("# Memory\n\n## 必须遵守\n- **Never** commit on `main`\n- a rule added later\n")
	later := NewInjector(ParseRules(dir))
	if got := later.Reminder("hi", view(0, false, system)); !strings.Contains(got, "a rule added later") {
		t.Errorf("a rule missing from the frozen prompt should be restated at once:\n%s", got)
	}
}
