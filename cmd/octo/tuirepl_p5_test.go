package main

import (
	"strings"
	"testing"
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

func TestTUI_PermissionPromptShowsFullCommand(t *testing.T) {
	m := newTestModel()
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
