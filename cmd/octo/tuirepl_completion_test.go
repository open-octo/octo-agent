package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// setInputAndComplete mirrors what a keystroke does: set the value, reset the
// selection, and re-filter the menu.
func setInputAndComplete(m *tuiModel, s string) {
	m.ta.SetValue(s)
	m.ta.CursorEnd()
	m.complIdx = 0
	m.updateCompletion()
}

func complNames(m *tuiModel) []string {
	out := make([]string, len(m.complItems))
	for i, it := range m.complItems {
		out[i] = it.name
	}
	return out
}

func TestCompletion_BareSlashListsAllBuiltins(t *testing.T) {
	m := newTestModel()
	setInputAndComplete(m, "/")
	if len(m.complItems) != len(builtinSlashCommands) {
		t.Fatalf("bare / should list all %d built-ins; got %d (%v)",
			len(builtinSlashCommands), len(m.complItems), complNames(m))
	}
	got := strings.Join(complNames(m), " ")
	for _, want := range []string{"/help", "/skills", "/mcp"} {
		if !strings.Contains(got, want) {
			t.Errorf("menu missing %s; got %s", want, got)
		}
	}
}

func TestCompletion_PrefixFilters(t *testing.T) {
	m := newTestModel()
	setInputAndComplete(m, "/s")
	// Built-ins starting with /s: /skills, /save, /sessions.
	if got := complNames(m); len(got) != 3 {
		t.Fatalf("/s should match /skills /save /sessions; got %v", got)
	}
	setInputAndComplete(m, "/me")
	if got := complNames(m); len(got) != 1 || got[0] != "/memory" {
		t.Errorf("/me should match only /memory; got %v", got)
	}
}

func TestCompletion_ClosedConditions(t *testing.T) {
	m := newTestModel()

	setInputAndComplete(m, "/help ") // trailing space → command token finished
	if m.complItems != nil {
		t.Errorf("menu should close once a space is typed; got %v", complNames(m))
	}
	setInputAndComplete(m, "hello") // no leading slash
	if m.complItems != nil {
		t.Errorf("menu should be closed for non-slash input; got %v", complNames(m))
	}
	m.turnRunning = true
	setInputAndComplete(m, "/")
	if len(m.complItems) != len(builtinSlashCommands) {
		t.Errorf("menu should stay open while a turn runs; got %v", complNames(m))
	}
}

func TestCompletion_IncludesSkills(t *testing.T) {
	m := newTestModel()
	m.cfg.skillReg = skillRegFor(t, map[string]string{
		"review": "---\nname: review\ndescription: Review the current diff\n---\nbody",
	})
	setInputAndComplete(m, "/rev")
	if got := complNames(m); len(got) != 1 || got[0] != "/review" {
		t.Fatalf("/rev should match the /review skill; got %v", got)
	}
	if m.complItems[0].desc != "Review the current diff" {
		t.Errorf("skill description = %q", m.complItems[0].desc)
	}
}

func TestCompletion_ViewRendersMenuAndHint(t *testing.T) {
	m := newTestModel()
	m.width = 80
	setInputAndComplete(m, "/sk")
	out := m.View()
	if !strings.Contains(out, "/skills") {
		t.Errorf("View should render the matching command; got:\n%s", out)
	}
	if !strings.Contains(out, "Enter run") {
		t.Errorf("View should render the completion key hint; got:\n%s", out)
	}
}

func TestCompletion_NavigationCycles(t *testing.T) {
	m := newTestModel()
	setInputAndComplete(m, "/s") // 3 matches, idx 0
	if len(m.complItems) != 3 {
		t.Fatalf("setup: want 3 matches, got %d", len(m.complItems))
	}
	down := func() { m.handleKey(tea.KeyMsg{Type: tea.KeyDown}) }
	down()
	if m.complIdx != 1 {
		t.Errorf("Down → idx 1, got %d", m.complIdx)
	}
	down()
	down() // 2 → 0 (cycle)
	if m.complIdx != 0 {
		t.Errorf("Down should cycle back to 0; got %d", m.complIdx)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyUp}) // 0 → 2 (wrap up)
	if m.complIdx != 2 {
		t.Errorf("Up should wrap to last (2); got %d", m.complIdx)
	}
}

// Tab fills the highlighted command into the input (to add arguments) without
// running it — Claude Code semantics.
func TestCompletion_TabFillsWithoutSubmitting(t *testing.T) {
	m := newTestModel()
	setInputAndComplete(m, "/sk") // → /skills, idx 0
	if len(m.complItems) == 0 {
		t.Fatal("setup: expected an open menu")
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyTab})

	if m.turnRunning {
		t.Error("Tab must fill the completion, not start a turn")
	}
	if got := m.ta.Value(); got != "/skills " {
		t.Errorf("filled value = %q, want %q", got, "/skills ")
	}
	if m.complItems != nil {
		t.Error("menu should close after filling")
	}
}

// Enter runs the highlighted command in one keystroke — no second Enter. The
// double-Enter flow (accept, then submit) was the main "sticky" complaint.
func TestCompletion_EnterRunsImmediately(t *testing.T) {
	m := newTestModel()
	setInputAndComplete(m, "/sk") // → /skills, idx 0
	if len(m.complItems) == 0 {
		t.Fatal("setup: expected an open menu")
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})

	if got := m.ta.Value(); got != "" {
		t.Errorf("input should be consumed by the dispatch, got %q", got)
	}
	if m.complItems != nil {
		t.Error("menu should close after running")
	}
	if m.turnRunning {
		t.Error("/skills is a local command — no turn should start")
	}
}

// A partial prefix + Enter must run the highlighted command, not send the
// half-typed prefix to the model as a prompt.
func TestCompletion_EnterRunsHighlightedNotPrefix(t *testing.T) {
	m := newTestModel()
	setInputAndComplete(m, "/exi") // → /exit, idx 0
	if len(m.complItems) != 1 {
		t.Fatalf("setup: want 1 match for /exi, got %v", complNames(m))
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.quit {
		t.Error("Enter on /exi should run /exit and quit")
	}
}
