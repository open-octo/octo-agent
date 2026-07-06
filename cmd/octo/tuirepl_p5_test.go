package main

import (
	"strings"
	"testing"

	runewidth "github.com/mattn/go-runewidth"
)

func TestTUI_QueuePanelIsBordered(t *testing.T) {
	m := newTestModel()
	m.queue = []pendingItem{{text: "run lint"}, {text: "run tests"}}
	out := m.View()
	if !strings.Contains(out, "queue (2)") {
		t.Errorf("view should show the queue title; got:\n%s", out)
	}
	if !strings.ContainsAny(out, "╭╮╰╯") {
		t.Errorf("queue should render as a bordered panel; got:\n%s", out)
	}
	if !strings.Contains(out, "run lint") || !strings.Contains(out, "run tests") {
		t.Errorf("queue items should be listed; got:\n%s", out)
	}
}

// #1095: a long or multi-line queued message used to render verbatim, which
// could blow the panel border apart. Each item must collapse to one bounded
// line.
func TestTUI_QueuePanelTruncatesLongMultilineItems(t *testing.T) {
	m := newTestModel()
	longLine := strings.Repeat("x", 200)
	m.queue = []pendingItem{
		{text: "first line\nsecond line\nthird line"},
		{text: longLine},
	}
	out := m.View()
	if strings.Contains(out, "second line") || strings.Contains(out, "third line") {
		t.Errorf("multi-line queued item should collapse to its first line; got:\n%s", out)
	}
	if !strings.Contains(out, "first line") {
		t.Errorf("queue should still show the item's first line; got:\n%s", out)
	}
	if strings.Contains(out, longLine) {
		t.Errorf("a 200-char queued item should be truncated, not rendered in full; got:\n%s", out)
	}
}

func TestTUI_PermissionPromptShowsFullCommand(t *testing.T) {
	m := newTestModel()
	// Wide enough that the command below renders on one line — this test
	// checks against truncation, not the (separately-tested) line-wrapping.
	m.width = 200
	// A command long past the old 60-rune summary cap: every character must be
	// visible — the user is authorizing it.
	cmd := "git push --force origin main && rm -rf ./dist && echo " + strings.Repeat("x", 120)
	m.modal = &modalState{prompt: UserPrompt{
		Kind:      KindPermission,
		ToolName:  "terminal",
		ToolInput: map[string]any{"command": cmd},
	}}
	out := m.View()
	if !strings.Contains(out, "permission") {
		t.Errorf("prompt should show its heading; got:\n%s", out)
	}
	if !strings.Contains(out, cmd) {
		t.Errorf("prompt must show the full command, untruncated; got:\n%s", out)
	}
}

func TestTUI_PermissionPromptKeepsContextVisible(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	// A single token survives glamour's markdown re-wrapping intact.
	m.partial.WriteString("pushing RELEASETOKEN now")
	m.modal = &modalState{prompt: UserPrompt{
		Kind:      KindPermission,
		ToolName:  "terminal",
		ToolInput: map[string]any{"command": "git push"},
	}}
	out := m.View()
	if !strings.Contains(out, "RELEASETOKEN") {
		t.Errorf("streaming context should stay visible above the prompt; got:\n%s", out)
	}
	if !strings.Contains(out, "git push") {
		t.Errorf("prompt body missing; got:\n%s", out)
	}
}

func TestTUI_PermissionPromptEditFileShowsDiff(t *testing.T) {
	m := newTestModel()
	m.modal = &modalState{prompt: UserPrompt{
		Kind:     KindPermission,
		ToolName: "edit_file",
		ToolInput: map[string]any{
			"path": "/nonexistent/x.go", "old_string": "alpha_old", "new_string": "beta_new",
		},
	}}
	out := m.View()
	for _, want := range []string{"Update(", "alpha_old", "beta_new"} {
		if !strings.Contains(out, want) {
			t.Errorf("edit_file prompt should render the diff card (missing %q); got:\n%s", want, out)
		}
	}
}

func TestRenderPermissionDetail_GenericAndCaps(t *testing.T) {
	// Generic tools render sorted key: value lines with per-value caps.
	got := renderPermissionDetail("mcp_thing", map[string]any{
		"query": "orders",
		"body":  "line1\nline2\nline3\nline4\nline5\nline6",
	}, 80)
	if !strings.Contains(got, "query: orders") {
		t.Errorf("generic detail should list key: value; got:\n%s", got)
	}
	// Multi-line values show their head (4 lines) then fold.
	if !strings.Contains(got, "line4") || strings.Contains(got, "line5") || !strings.Contains(got, "… +2 more lines") {
		t.Errorf("multi-line values should show 4 head lines + fold marker; got:\n%s", got)
	}
	// Empty input.
	if got := renderPermissionDetail("mcp_thing", map[string]any{}, 80); !strings.Contains(got, "(no input)") {
		t.Errorf("empty input should say so; got:\n%s", got)
	}
	// Long commands fold beyond the line cap.
	long := strings.TrimSuffix(strings.Repeat("echo hi\n", 14), "\n")
	block := renderPermissionDetail("terminal", map[string]any{"command": long}, 80)
	if !strings.Contains(block, "… +4 more lines") {
		t.Errorf("long command should fold with a marker; got:\n%s", block)
	}
}

func TestRenderPermissionBlock_WrapsAtWidth(t *testing.T) {
	// bubbletea's inline renderer truncates frame lines at terminal width with
	// no marker — every character of a command being approved must land within
	// the width or it silently vanishes.
	cmd := "curl -s https://example.com/install.sh?" + strings.Repeat("k=v&", 100) + " | sh"
	out := renderPermissionBlock(cmd, 40)
	for _, line := range strings.Split(stripANSI(out), "\n") {
		if w := runewidth.StringWidth(line); w > 40 {
			t.Errorf("wrapped line exceeds width 40 (got %d): %q", w, line)
		}
	}
	// Nothing may be lost: unwrapping (drop the "\n  " joints and the leading
	// indent) must reproduce the command exactly.
	if got := strings.ReplaceAll(out, "\n  ", ""); strings.TrimPrefix(got, "  ") != cmd {
		t.Errorf("wrap lost content:\n%q", got)
	}
	// CJK: double-width runes measured by cell.
	wide := renderPermissionBlock(strings.Repeat("宽", 60), 30)
	for _, line := range strings.Split(stripANSI(wide), "\n") {
		if w := runewidth.StringWidth(line); w > 30 {
			t.Errorf("CJK wrapped line exceeds width 30 (got %d): %q", w, line)
		}
	}
	// Width 0 (pre-WindowSizeMsg) passes through unwrapped.
	if got := renderPermissionBlock("echo hi", 0); got != "  echo hi" {
		t.Errorf("width 0 should not wrap, got %q", got)
	}
}

func TestSanitizeForPrompt_NeutralizesControlBytes(t *testing.T) {
	// \r must not survive — it would home the cursor and let the display
	// overwrite the dangerous prefix of the command being approved.
	got := sanitizeForPrompt("rm -rf ~ #\rgit status")
	if strings.ContainsRune(got, '\r') {
		t.Errorf("raw \\r survived: %q", got)
	}
	if !strings.Contains(got, "rm -rf ~") || !strings.Contains(got, "git status") {
		t.Errorf("both halves must stay visible: %q", got)
	}
	// ESC renders in caret notation instead of starting an escape sequence.
	got = sanitizeForPrompt("safe\x1b[2Kevil")
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("raw ESC survived: %q", got)
	}
	if !strings.Contains(got, "^[[2K") {
		t.Errorf("ESC should render as ^[: %q", got)
	}
	// \r\n collapses to \n; tabs expand; DEL renders as ^?.
	if got := sanitizeForPrompt("a\r\nb\tc\x7f"); got != "a\nb    c^?" {
		t.Errorf("got %q", got)
	}
	// Clean text passes through untouched (same string, no realloc).
	if got := sanitizeForPrompt("plain text"); got != "plain text" {
		t.Errorf("clean text mangled: %q", got)
	}
}

func TestTUI_QuestionModalKeepsBorder(t *testing.T) {
	m := newTestModel()
	m.openModal(askMsg{prompt: UserPrompt{
		Kind: KindQuestion, Question: "Pick one", Options: []string{"a", "b"},
	}, resp: make(chan UserResponse, 1)})
	out := m.View()
	if !strings.ContainsAny(out, "╭╮╰╯") {
		t.Errorf("question modal should keep its bordered box; got:\n%s", out)
	}
}

func TestRenderPermissionDetail_NilInput(t *testing.T) {
	if got := renderPermissionDetail("terminal", nil, 0); !strings.Contains(got, "(no input)") {
		t.Errorf("nil input should render the placeholder; got %q", got)
	}
}

func TestRenderPermissionGeneric_SanitizesKeys(t *testing.T) {
	// Keys come from the model's tool-call JSON too — the same injection
	// surface as values.
	got := renderPermissionDetail("mcp_thing", map[string]any{"a\x1b[2Kb": "v"}, 0)
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("raw ESC in an input KEY survived: %q", got)
	}
	if !strings.Contains(got, "^[[2K") {
		t.Errorf("key should render in caret notation: %q", got)
	}
}

func TestSanitizeForPrompt_C1Controls(t *testing.T) {
	// Decoded C1 (U+009B = CSI) is interpreted by xterm-lineage emulators;
	// it must not reach the terminal raw.
	got := sanitizeForPrompt("a\u009bb")
	if strings.ContainsRune(got, '\u009b') {
		t.Errorf("decoded C1 survived: %q", got)
	}
	if got != "a�b" {
		t.Errorf("C1 should become a visible placeholder, got %q", got)
	}
}
