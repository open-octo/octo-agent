package tui

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	outBullet    = lipgloss.NewStyle().Foreground(ColAccent).SetString("●")
	outBulletErr = lipgloss.NewStyle().Foreground(ColDanger).SetString("●")
	outGutter    = lipgloss.NewStyle().Foreground(ColDim).SetString("│")
	outMore      = lipgloss.NewStyle().Foreground(ColMuted)
)

// RenderOutputCard renders a tool's textual output as a card: a header row
// (● verb(target)) above the output, each line behind a "│" gutter, capped to
// maxLines (<=0 = no cap) with an "… +N lines" marker for the remainder.
// isErr tints the bullet red. Empty output renders "(no output)". The trailing
// newline is omitted so callers control spacing (mirrors Card.Render).
// When language is non-empty, each line is syntax-highlighted with Chroma.
func RenderOutputCard(verb, target, output string, maxLines int, isErr bool, language string) string {
	bullet := outBullet
	if isErr {
		bullet = outBulletErr
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s %s", bullet, headerVerb.Render(fmt.Sprintf("%s(%s)", verb, target))))

	all := splitLinesNoTrail(output)
	// Blank lines waste preview slots without adding information; filter them
	// out so the cap applies to meaningful content only.
	lines := all[:0]
	for _, l := range all {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		b.WriteString("\n  " + outGutter.String() + " " + outMore.Render("(no output)"))
		return b.String()
	}

	shown, extra := lines, 0
	if maxLines > 0 && len(lines) > maxLines {
		shown, extra = lines[:maxLines], len(lines)-maxLines
	}

	dark := IsDark()
	for _, ln := range shown {
		body := expandTabs(ln)
		if language != "" {
			body = highlightLine(body, language, dark)
		}
		b.WriteString("\n  " + outGutter.String() + " " + body)
	}
	if extra > 0 {
		b.WriteString("\n  " + outMore.Render("… +"+pluralise(extra, "line")))
	}
	return b.String()
}

// RenderToolStatus renders a tool call that has no body card as a single
// header-style line — "● name(target)" — so card and non-card tools share one
// visual language. isErr tints the bullet red and appends errText dimmed.
func RenderToolStatus(name, target string, isErr bool, errText string) string {
	bullet := outBullet
	if isErr {
		bullet = outBulletErr
	}
	s := fmt.Sprintf("%s %s", bullet, headerVerb.Render(fmt.Sprintf("%s(%s)", name, target)))
	if isErr && errText != "" {
		s += " " + outMore.Render("— "+errText)
	}
	return s
}

var linkText = lipgloss.NewStyle().Underline(true)

// Hyperlink wraps text in an OSC 8 terminal hyperlink. Terminals that support
// OSC 8 (iTerm2, Windows Terminal, kitty, WezTerm) make the text click-to-open;
// others ignore the escape sequences and show the plain text — a safe fallback.
func Hyperlink(uri, text string) string {
	return "\x1b]8;;" + uri + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// FileURI converts an absolute filesystem path to a file:// URI, percent-
// encoding as needed. Windows drive paths gain the required leading slash
// (C:\x → file:///C:/x); UNC paths put the server in the URI authority
// (\\server\share\x → file://server/share/x). Semicolons are percent-encoded
// even though URIs allow them raw: the OSC 8 escape that carries this URI is
// itself semicolon-delimited, and a raw one truncates the link in strict
// terminal parsers.
func FileURI(abs string) string {
	p := filepath.ToSlash(abs)
	u := url.URL{Scheme: "file"}
	if rest, ok := strings.CutPrefix(p, "//"); ok {
		host, path, _ := strings.Cut(rest, "/")
		u.Host, u.Path = host, "/"+path
	} else {
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		u.Path = p
	}
	return strings.ReplaceAll(u.String(), ";", "%3B")
}

// RenderArtifactStatus renders show_artifact's success line: the path as an
// underlined click-to-open file:// hyperlink, so a TUI user can open the
// artifact directly instead of hunting the file down by hand.
func RenderArtifactStatus(absPath string) string {
	return fmt.Sprintf("%s %s %s", outBullet, headerVerb.Render("Artifact"),
		Hyperlink(FileURI(absPath), linkText.Render(absPath)))
}
