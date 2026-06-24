package tui

import (
	"fmt"
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
