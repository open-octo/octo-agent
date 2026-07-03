package tui

import (
	"regexp"
	"strings"
	"testing"

	rw "github.com/mattn/go-runewidth"
)

// stripANSI removes CSI escape sequences so tests can measure visible width.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func TestRenderOutputCard_HeaderAndBody(t *testing.T) {
	got := RenderOutputCard("Run", "go test ./...", "ok  pkg/a  0.2s\nok  pkg/b  0.1s", OutputCardOpts{})
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
	got := RenderOutputCard("Search", "foo", strings.Join(lines, "\n"), OutputCardOpts{MaxLines: 5})
	if !strings.Contains(got, "… +15 lines") {
		t.Errorf("expected '… +15 lines' marker; got:\n%s", got)
	}
	// Exactly 5 body lines shown (count the gutter glyph occurrences).
	if n := strings.Count(got, "│"); n != 5 {
		t.Errorf("expected 5 gutter rows, got %d:\n%s", n, got)
	}
}

func TestRenderOutputCard_EmptyOutput(t *testing.T) {
	got := RenderOutputCard("Run", "true", "", OutputCardOpts{})
	if !strings.Contains(got, "(no output)") {
		t.Errorf("expected '(no output)'; got:\n%s", got)
	}
}

func TestRenderOutputCard_SingularMoreLine(t *testing.T) {
	got := RenderOutputCard("Run", "x", "a\nb\nc", OutputCardOpts{MaxLines: 2})
	if !strings.Contains(got, "… +1 line") || strings.Contains(got, "1 lines") {
		t.Errorf("expected singular '… +1 line'; got:\n%s", got)
	}
}

func TestRenderOutputCard_ClampsLineWidth(t *testing.T) {
	long := strings.Repeat("x", 500)
	cjk := strings.Repeat("宽", 200) // double-width runes
	got := RenderOutputCard("Run", "cmd", long+"\n"+cjk, OutputCardOpts{Width: 40})
	for _, line := range strings.Split(stripANSI(got), "\n") {
		if w := rw.StringWidth(line); w > 40 {
			t.Errorf("row exceeds width 40 (got %d): %q", w, line)
		}
	}
	if !strings.Contains(got, "…") {
		t.Errorf("clamped rows should end in an ellipsis; got:\n%s", got)
	}
}

func TestRenderOutputCard_ClampsHeader(t *testing.T) {
	target := strings.Repeat("a", 200)
	got := RenderOutputCard("Run", target, "out", OutputCardOpts{Width: 40, Meta: "1.2s"})
	header := strings.Split(stripANSI(got), "\n")[0]
	if w := rw.StringWidth(header); w > 40 {
		t.Errorf("header exceeds width 40 (got %d): %q", w, header)
	}
	if !strings.Contains(header, "(1.2s)") {
		t.Errorf("meta should survive header clamping; got: %q", header)
	}
}

func TestRenderOutputCard_TailMode(t *testing.T) {
	got := RenderOutputCard("Run", "cmd", "first\nsecond\nthird\nfourth\nfifth", OutputCardOpts{MaxLines: 2, Tail: true})
	if strings.Contains(got, "first") || !strings.Contains(got, "fifth") {
		t.Errorf("tail mode should keep the last lines and fold the head; got:\n%s", got)
	}
	if !strings.Contains(got, "… +3 lines") {
		t.Errorf("expected fold marker; got:\n%s", got)
	}
	if strings.Index(got, "… +3 lines") > strings.Index(got, "fourth") {
		t.Errorf("tail-mode marker should precede the body; got:\n%s", got)
	}
}

func TestRenderOutputCard_MetaInHeader(t *testing.T) {
	got := RenderOutputCard("Run", "make", "ok", OutputCardOpts{Meta: "exit 1 · 12s"})
	if !strings.Contains(got, "(exit 1 · 12s)") {
		t.Errorf("meta annotation missing from header; got:\n%s", got)
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
		got := RenderOutputCard("Read", "main.go", output, OutputCardOpts{Language: lang})
		if strings.ContainsRune(got, '\t') {
			t.Errorf("lang=%q: rendered card still contains a raw tab:\n%q", lang, got)
		}
	}
}

func TestRenderOutputCard_Highlight(t *testing.T) {
	output := "package main\n\nfunc main() {}"
	got := RenderOutputCard("Read", "main.go", output, OutputCardOpts{Language: "go"})
	// Chroma highlighting should inject ANSI escape codes.
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("expected ANSI escapes from Chroma highlighting; got:\n%s", got)
	}
}

func TestFileURI(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/tmp/report.html", "file:///tmp/report.html"},
		{"/tmp/my report.html", "file:///tmp/my%20report.html"},
		// A Windows drive path (already slashed — filepath.ToSlash converts the
		// separators on Windows itself) gains the required leading slash.
		{"C:/Users/a/chart.html", "file:///C:/Users/a/chart.html"},
		// UNC: server goes in the URI authority, not a 4-slash legacy form.
		{"//server/share/x.html", "file://server/share/x.html"},
		// Raw ';' would truncate the OSC 8 escape in strict parsers.
		{"/tmp/a;b.html", "file:///tmp/a%3Bb.html"},
	}
	for _, c := range cases {
		if got := FileURI(c.in); got != c.want {
			t.Errorf("FileURI(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenderArtifactStatus(t *testing.T) {
	got := RenderArtifactStatus("/tmp/report.html")
	// OSC 8 open + URI, the visible path, and the OSC 8 close must all be there;
	// terminals without OSC 8 support ignore the escapes and show the path.
	for _, want := range []string{
		"\x1b]8;;file:///tmp/report.html\x1b\\",
		"/tmp/report.html",
		"\x1b]8;;\x1b\\",
		"Artifact",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("artifact status missing %q; got:\n%q", want, got)
		}
	}
}
