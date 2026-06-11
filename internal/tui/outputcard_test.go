package tui

import (
	"strings"
	"testing"
)

func TestRenderOutputCard_HeaderAndBody(t *testing.T) {
	got := RenderOutputCard("Run", "go test ./...", "ok  pkg/a  0.2s\nok  pkg/b  0.1s", 0, false, "")
	for _, want := range []string{"Run(go test ./...)", "ok  pkg/a  0.2s", "ok  pkg/b  0.1s"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderOutputCard_CapsWithMoreMarker(t *testing.T) {
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "line")
	}
	got := RenderOutputCard("Search", "foo", strings.Join(lines, "\n"), 5, false, "")
	if !strings.Contains(got, "… +15 lines") {
		t.Errorf("expected '… +15 lines' marker; got:\n%s", got)
	}
	// Exactly 5 body lines shown (count the gutter glyph occurrences).
	if n := strings.Count(got, "│"); n != 5 {
		t.Errorf("expected 5 gutter rows, got %d:\n%s", n, got)
	}
}

func TestRenderOutputCard_EmptyOutput(t *testing.T) {
	got := RenderOutputCard("Run", "true", "", 0, false, "")
	if !strings.Contains(got, "(no output)") {
		t.Errorf("expected '(no output)'; got:\n%s", got)
	}
}

func TestRenderOutputCard_SingularMoreLine(t *testing.T) {
	got := RenderOutputCard("Run", "x", "a\nb\nc", 2, false, "")
	if !strings.Contains(got, "… +1 line") || strings.Contains(got, "1 lines") {
		t.Errorf("expected singular '… +1 line'; got:\n%s", got)
	}
}

func TestRenderToolStatus(t *testing.T) {
	got := RenderToolStatus("mcp_thing", "q=x", false, "")
	if !strings.Contains(got, "mcp_thing(q=x)") {
		t.Errorf("missing header in:\n%s", got)
	}
	gotErr := RenderToolStatus("mcp_thing", "q=x", true, "boom")
	if !strings.Contains(gotErr, "— boom") {
		t.Errorf("error status should append the error; got:\n%s", gotErr)
	}
}

func TestRenderOutputCard_ExpandsTabs(t *testing.T) {
	// read_file emits "%6d\t<line>" and indents Go with tabs. Raw tabs in a
	// card row skip cells without painting them, letting the live view below
	// bleed through. The renderer must expand every tab to spaces.
	output := "     1\tpackage main\n     2\t\tfield int"
	for _, lang := range []string{"", "go"} {
		got := RenderOutputCard("Read", "main.go", output, 0, false, lang)
		if strings.ContainsRune(got, '\t') {
			t.Errorf("lang=%q: rendered card still contains a raw tab:\n%q", lang, got)
		}
	}
}

func TestRenderOutputCard_Highlight(t *testing.T) {
	output := "package main\n\nfunc main() {}"
	got := RenderOutputCard("Read", "main.go", output, 0, false, "go")
	// Chroma highlighting should inject ANSI escape codes.
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("expected ANSI escapes from Chroma highlighting; got:\n%s", got)
	}
}
