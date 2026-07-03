// Package tui renders agent tool events as visual cards for the terminal
// REPL. The first card type is the diff card emitted for `edit_file`:
// two header rows (`● Update(path)` / `└ Added N, removed M`) above a
// line-numbered diff body with red/green washes for `-`/`+` rows.
//
// The renderer composes Lipgloss for layout and Chroma for syntax
// highlighting. To keep highlighting visible under the row backgrounds we
// re-emit the background ANSI escape after each `\x1b[0m` reset Chroma
// emits inside a row — see renderRow.
package tui

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
)

// Line is one row of a rendered diff card.
type Line struct {
	Num   int    // 1-based line number; 0 = no number column
	Kind  rune   // ' ' = unchanged, '+' = added, '-' = removed
	Text  string // raw source line, pre-highlight
	DimNo bool   // dim the line-number column (used for unchanged rows)
}

// Card holds everything needed to render one tool-result card.
//
// Verb is the action being reported ("Update", "Create", …); Path is the
// file the action targets. Added / Removed feed the "└ Added N, removed M"
// summary row. Language is the Chroma lexer name (e.g. "go", "python") —
// empty disables highlighting.
type Card struct {
	Verb     string
	Path     string
	Added    int
	Removed  int
	Lines    []Line
	Language string
	// Width, when >0, clips each row (header included) to the terminal width
	// so a long source line can't soft-wrap the card into many screen rows.
	Width int
}

// Render returns the card as an ANSI-coloured string. The trailing newline
// is omitted so callers can decide on spacing.
func (c Card) Render() string {
	var b strings.Builder

	dark := IsDark() // probe once per card; threads to the row washes + Chroma style
	b.WriteString(renderHeader(c.Verb, c.Path, c.Width))
	b.WriteString("\n")
	b.WriteString(renderSummary(c.Added, c.Removed))
	b.WriteString("\n")
	for _, ln := range c.Lines {
		b.WriteString(renderRow(ln, c.Language, dark, c.Width))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// diffCardMaxRows caps each side (removed, added) of an edit_file diff card;
// the remainder collapses into an "… +N lines" row so a large edit doesn't
// fill the screen. Per side rather than total, so a big rewrite still shows
// the head of both the old and the new text.
const diffCardMaxRows = 6

// ellipsisKind marks a Line as a collapsed-rows "… +N lines" marker rather
// than diff content; renderRow draws it dimmed, with no wash or highlighting.
const ellipsisKind = '…'

// RenderEditCard builds and renders a card for an `edit_file` invocation.
// It reads filePath to compute the line number where newString lands; if
// the read fails (file moved, perms, etc.) the card still renders, just
// without line numbers. Language is inferred from the file extension.
// width > 0 clips each row to the terminal width.
func RenderEditCard(filePath, oldString, newString string, width int) string {
	added := nonEmptyLineCount(newString)
	removed := nonEmptyLineCount(oldString)

	startLine := 0
	if data, err := os.ReadFile(filePath); err == nil {
		// Normalize CRLF so a \n-only needle (callers may strip \r for
		// display safety) still locates its line in a CRLF file.
		data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
		needle := []byte(strings.ReplaceAll(newString, "\r\n", "\n"))
		if idx := bytes.Index(data, needle); idx >= 0 {
			startLine = 1 + bytes.Count(data[:idx], []byte("\n"))
		}
	}

	lines := make([]Line, 0, removed+added)
	lines = appendDiffRows(lines, splitLinesNoTrail(oldString), '-', startLine)
	lines = appendDiffRows(lines, splitLinesNoTrail(newString), '+', startLine)

	c := Card{
		Verb:     "Update",
		Path:     filePath,
		Added:    added,
		Removed:  removed,
		Lines:    lines,
		Language: GuessLanguage(filePath),
		Width:    width,
	}
	return c.Render()
}

// appendDiffRows appends one side of the diff, capped at diffCardMaxRows with
// an ellipsis row for the remainder. Rows are numbered from startLine (0 =
// no number column).
func appendDiffRows(lines []Line, src []string, kind rune, startLine int) []Line {
	shown, extra := src, 0
	if len(src) > diffCardMaxRows {
		shown, extra = src[:diffCardMaxRows], len(src)-diffCardMaxRows
	}
	for i, t := range shown {
		num := 0
		if startLine > 0 {
			num = startLine + i
		}
		lines = append(lines, Line{Num: num, Kind: kind, Text: t})
	}
	if extra > 0 {
		lines = append(lines, Line{Kind: ellipsisKind, Text: "… +" + pluralise(extra, "line")})
	}
	return lines
}

// ─── internals ────────────────────────────────────────────────────────────

// Foreground styles draw from the shared adaptive palette (theme.go) so they
// read on light and dark terminals; lipgloss resolves the side at render time.
var (
	headerBullet  = lipgloss.NewStyle().Foreground(ColAccent).SetString("●")
	headerVerb    = lipgloss.NewStyle().Bold(true)
	headerSummary = lipgloss.NewStyle().Foreground(ColMuted)

	lineNoStyle = lipgloss.NewStyle().Foreground(ColDim).Width(4).Align(lipgloss.Right)
	lineNoDim   = lineNoStyle.Foreground(ColDimmer)

	plusMark  = lipgloss.NewStyle().Foreground(ColAccent).SetString("+")
	minusMark = lipgloss.NewStyle().Foreground(ColDanger).SetString("-")
)

// Raw 24-bit ANSI background washes for +/- rows. Raw codes (not Lipgloss)
// because Chroma emits `\x1b[0m` resets mid-line; re-applying the background
// after each reset is simpler than coaxing Lipgloss to do it. Light/dark
// variants are picked together with the Chroma style (see bgWash / chromaStyle)
// so foreground text stays legible against the wash.
const (
	bgAddedDark    = "\x1b[48;2;14;58;28m"    // deep green
	bgRemovedDark  = "\x1b[48;2;74;18;18m"    // deep red
	bgAddedLight   = "\x1b[48;2;230;255;236m" // pale green (#E6FFEC)
	bgRemovedLight = "\x1b[48;2;255;235;233m" // pale red (#FFEBE9)
	bgReset        = "\x1b[49m"
	resetAll       = "\x1b[0m"
	clearEOL       = "\x1b[K" // paints the row background to the right margin
)

// bgWash returns the row-background escape for a +/- line, matched to the
// terminal background.
func bgWash(kind rune, dark bool) string {
	switch {
	case kind == '-' && dark:
		return bgRemovedDark
	case kind == '-':
		return bgRemovedLight
	case dark:
		return bgAddedDark
	default:
		return bgAddedLight
	}
}

// chromaStyle picks a syntax-highlight style legible against the wash.
func chromaStyle(dark bool) string {
	if dark {
		return "github-dark"
	}
	return "github"
}

func renderHeader(verb, path string, width int) string {
	return fmt.Sprintf("%s %s", headerBullet, renderCardHeader(verb, path, "", width))
}

func renderSummary(added, removed int) string {
	return "  " + headerSummary.Render(
		fmt.Sprintf("└ Added %s, removed %s",
			pluralise(added, "line"), pluralise(removed, "line")),
	)
}

// diffGutterWidth is the visible width of a diff row's prefix — leading
// space, 4-cell line-number column, space, +/- mark, space — that the body
// must share the terminal row with.
const diffGutterWidth = 8

func renderRow(ln Line, language string, dark bool, width int) string {
	if ln.Kind == ellipsisKind {
		return "  " + outMore.Render(ln.Text)
	}
	noCol := renderLineNo(ln)
	mark := renderMark(ln.Kind)
	// Clip before highlighting — Chroma output is self-contained per token,
	// so cutting afterwards could strand an open escape (see RenderOutputCard).
	body := clipLine(expandTabs(ln.Text), width-diffGutterWidth)
	if language != "" {
		body = highlightLine(body, language, dark)
	}

	if ln.Kind == ' ' {
		return fmt.Sprintf(" %s %s %s", noCol, mark, body)
	}

	bg := bgWash(ln.Kind, dark)
	// Re-apply background after each reset so highlighting tokens don't
	// punch holes in the row colour.
	body = strings.ReplaceAll(body, resetAll, resetAll+bg)
	return fmt.Sprintf("%s %s %s %s%s%s%s", bg, noCol, mark, body, clearEOL, bgReset, resetAll)
}

func renderLineNo(ln Line) string {
	style := lineNoStyle
	if ln.DimNo {
		style = lineNoDim
	}
	s := ""
	if ln.Num > 0 {
		s = fmt.Sprintf("%d", ln.Num)
	}
	return style.Render(s)
}

func renderMark(kind rune) string {
	switch kind {
	case '+':
		return plusMark.String()
	case '-':
		return minusMark.String()
	default:
		return " "
	}
}

// highlightLine returns the input with Chroma syntax-highlighting ANSI
// escapes applied. On any failure (unknown lexer, format error) the raw
// input is returned unchanged.
func highlightLine(src, language string, dark bool) string {
	lex := lexers.Get(language)
	if lex == nil {
		return src
	}
	style := styles.Get(chromaStyle(dark))
	if style == nil {
		style = styles.Fallback
	}
	formatter := formatters.Get("terminal16m")
	if formatter == nil {
		return src
	}
	it, err := lex.Tokenise(nil, src)
	if err != nil {
		return src
	}
	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, it); err != nil {
		return src
	}
	return strings.TrimRight(buf.String(), "\n")
}

// GuessLanguage maps a path extension to a Chroma lexer name. Empty
// return disables highlighting.
func GuessLanguage(path string) string {
	switch {
	case strings.HasSuffix(path, ".go"):
		return "go"
	case strings.HasSuffix(path, ".py"):
		return "python"
	case strings.HasSuffix(path, ".js"), strings.HasSuffix(path, ".jsx"):
		return "javascript"
	case strings.HasSuffix(path, ".ts"), strings.HasSuffix(path, ".tsx"):
		return "typescript"
	case strings.HasSuffix(path, ".rs"):
		return "rust"
	case strings.HasSuffix(path, ".rb"):
		return "ruby"
	case strings.HasSuffix(path, ".md"):
		return "markdown"
	case strings.HasSuffix(path, ".sh"), strings.HasSuffix(path, ".bash"):
		return "bash"
	case strings.HasSuffix(path, ".json"):
		return "json"
	case strings.HasSuffix(path, ".yaml"), strings.HasSuffix(path, ".yml"):
		return "yaml"
	}
	return ""
}

// tabStop is the column width a tab advances to. 8 matches the terminal
// default and `cat -n`, so expanded rows line up exactly as a raw tab would
// have — minus the bleed-through.
const tabStop = 8

// expandTabs replaces tab characters with spaces up to the next tab stop. A
// card row must paint every cell it occupies: a raw tab repositions the cursor
// without overwriting the cells it skips, so stale content from the live view
// rendered below (spinner, status bar, ghost text) shows through the gap. The
// read_file "%6d\t" line-number separator and tab-indented source are the
// common sources. Column counting starts at the body's own column 0; the small
// shift from the card's gutter prefix doesn't matter once no real tabs remain.
func expandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := tabStop - col%tabStop
			for i := 0; i < n; i++ {
				b.WriteByte(' ')
			}
			col += n
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}

// splitLinesNoTrail splits s on '\n' but drops the trailing empty element
// that strings.Split would produce when s ends in '\n'. A genuine blank
// line in the middle of s is preserved.
func splitLinesNoTrail(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// nonEmptyLineCount returns how many lines in s contain at least one
// non-whitespace character. Blank lines are still part of the diff body
// but don't count toward "added N, removed M" — which matches Claude
// Code's behaviour for the summary row.
func nonEmptyLineCount(s string) int {
	n := 0
	for _, line := range splitLinesNoTrail(s) {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

func pluralise(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
