package main

import (
	"strings"
	"testing"
	"time"
)

func TestCardVerbFor(t *testing.T) {
	cases := map[string]string{
		"edit_file":       "Update",
		"terminal":        "Run",
		"terminal_output": "Check",
		"kill_shell":      "Kill",
		"terminal_input":  "Input",
		"grep":            "Grep",
		"web_search":      "Search",
		"glob":            "Glob",
		"read_file":       "Read",
		"write_file":      "Write",
		"web_fetch":       "Fetch",
		"sub_agent":       "", // not a card tool → one-liner
		"remember":        "",
	}
	for tool, want := range cases {
		if got := cardVerbFor(tool); got != want {
			t.Errorf("cardVerbFor(%q) = %q, want %q", tool, got, want)
		}
	}
}

func TestCardTargetFor(t *testing.T) {
	cases := []struct {
		tool  string
		input map[string]any
		want  string
	}{
		{"terminal", map[string]any{"command": "go test ./..."}, "go test ./..."},
		{"grep", map[string]any{"pattern": "TODO"}, "TODO"},
		{"web_search", map[string]any{"query": "golang generics"}, "golang generics"},
		{"read_file", map[string]any{"path": "main.go"}, "main.go"},
		{"web_fetch", map[string]any{"url": "https://x.example"}, "https://x.example"},
		// terminal_output names the process by its command; with no live process
		// to resolve it falls back to the bare internal id (never "id (cmd)").
		{"terminal_output", map[string]any{"id": "bg_404"}, "bg_404"},
	}
	for _, c := range cases {
		if got := cardTargetFor(c.tool, c.input); got != c.want {
			t.Errorf("cardTargetFor(%q, %v) = %q, want %q", c.tool, c.input, got, c.want)
		}
	}
}

func TestRenderToolCard_Dispatch(t *testing.T) {
	// edit_file success → diff card.
	edit := renderToolCard("edit_file", map[string]any{
		"path": "/tmp/nope.go", "old_string": "alpha", "new_string": "beta",
	}, "", false, 0, 0)
	if !strings.Contains(edit, "Update(") || !strings.Contains(edit, "alpha") || !strings.Contains(edit, "beta") {
		t.Errorf("edit_file should render a diff card; got:\n%s", edit)
	}

	// terminal → output card with the command + output.
	run := renderToolCard("terminal", map[string]any{"command": "ls"}, "file_a\nfile_b", false, 0, 0)
	if !strings.Contains(run, "Run(ls)") || !strings.Contains(run, "file_a") {
		t.Errorf("terminal should render an output card; got:\n%s", run)
	}

	// terminal_output → snapshot card with status + tail.
	out := renderToolCard("terminal_output", map[string]any{"id": "bg_1"}, "[status: running]\nstarting up", false, 0, 0)
	if !strings.Contains(out, "starting up") {
		t.Errorf("terminal_output should render an output card; got:\n%s", out)
	}

	// non-card tool → "".
	if got := renderToolCard("sub_agent", map[string]any{}, "x", false, 0, 0); got != "" {
		t.Errorf("non-card tool should return \"\"; got %q", got)
	}
}

func TestRenderToolCard_TerminalShowsTail(t *testing.T) {
	// Command output is shown from the tail — the head is the least
	// informative end (errors and summaries land at the bottom).
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, strings.Repeat("x", 3)+"_line_"+string(rune('0'+i%10)))
	}
	got := renderToolCard("terminal", map[string]any{"command": "make test"}, strings.Join(lines, "\n"), false, 0, 0)
	if strings.Contains(got, "xxx_line_1") {
		t.Errorf("head line should be folded away in tail mode; got:\n%s", got)
	}
	if !strings.Contains(got, "xxx_line_0") { // the 10th (last) line
		t.Errorf("last line should be visible in tail mode; got:\n%s", got)
	}
	if !strings.Contains(got, "… +6 lines") {
		t.Errorf("fold marker missing; got:\n%s", got)
	}
	// Tail mode puts the marker ABOVE the body.
	if strings.Index(got, "… +6 lines") > strings.Index(got, "xxx_line_0") {
		t.Errorf("tail-mode marker should precede the body; got:\n%s", got)
	}
}

func TestRenderToolCard_TerminalExitLiftedToHeader(t *testing.T) {
	out := "compiling\nfailure detail\n[exit: 1]"
	got := renderToolCard("terminal", map[string]any{"command": "make"}, out, false, 0, 1500*time.Millisecond)
	if !strings.Contains(got, "exit 1") {
		t.Errorf("exit code should surface in the header meta; got:\n%s", got)
	}
	if !strings.Contains(got, "1.5s") {
		t.Errorf("elapsed should surface in the header meta; got:\n%s", got)
	}
	if strings.Contains(got, "[exit: 1]") {
		t.Errorf("raw exit marker should be stripped from the body; got:\n%s", got)
	}
	if !strings.Contains(got, "failure detail") {
		t.Errorf("body should keep the real output; got:\n%s", got)
	}
}

func TestSplitTerminalExit(t *testing.T) {
	cases := []struct{ in, wantBody, wantExit string }{
		{"out\n[exit: 1]", "out", "1"},
		{"out\n[exit: signal: killed]", "out", "signal: killed"},
		{"[exit: 2]", "", "2"},
		{"out\n[exit: 1]\ntrailing", "out\n[exit: 1]\ntrailing", ""}, // marker not last → untouched
		{"plain output", "plain output", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		body, exit := splitTerminalExit(c.in)
		if body != c.wantBody || exit != c.wantExit {
			t.Errorf("splitTerminalExit(%q) = (%q, %q), want (%q, %q)", c.in, body, exit, c.wantBody, c.wantExit)
		}
	}
}

func TestFormatElapsed(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, ""},
		{-time.Second, ""},
		{1234 * time.Millisecond, "1.2s"},
		{94 * time.Second, "1m34s"},
	}
	for _, c := range cases {
		if got := formatElapsed(c.in); got != c.want {
			t.Errorf("formatElapsed(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRendersCard_PlainDisablesCards(t *testing.T) {
	plainOff := &tuiModel{cfg: replConfig{plain: false}}
	if !plainOff.rendersCard("edit_file") {
		t.Error("with --plain off, edit_file should render as a card")
	}
	plainOn := &tuiModel{cfg: replConfig{plain: true}}
	if plainOn.rendersCard("edit_file") {
		t.Error("with --plain on, no tool should render as a card")
	}
	// terminal_output is now a card tool.
	if !plainOff.rendersCard("terminal_output") {
		t.Error("with --plain off, terminal_output should render as a card")
	}
	// A non-card tool is never a card regardless of --plain.
	if plainOff.rendersCard("sub_agent") {
		t.Error("sub_agent is not a card tool")
	}
}
