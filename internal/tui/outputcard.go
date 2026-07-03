package tui

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	rw "github.com/mattn/go-runewidth"
	"github.com/muesli/reflow/truncate"
)

var (
	outBullet    = lipgloss.NewStyle().Foreground(ColAccent).SetString("●")
	outBulletErr = lipgloss.NewStyle().Foreground(ColDanger).SetString("●")
	outGutter    = lipgloss.NewStyle().Foreground(ColDim).SetString("│")
	outMore      = lipgloss.NewStyle().Foreground(ColMuted)
)

// OutputCardOpts controls how RenderOutputCard clips and annotates the body.
type OutputCardOpts struct {
	// MaxLines caps how many output lines the card shows (<=0 = no cap); the
	// remainder collapses into an "… +N lines" marker.
	MaxLines int
	// Width, when >0, clips every rendered row (header included) to the
	// terminal width so one long line can't soft-wrap into dozens of screen
	// rows and defeat the MaxLines cap.
	Width int
	// Tail shows the LAST MaxLines instead of the first — the right end for
	// command output, where errors and summaries land at the bottom. The
	// "… +N lines" marker moves above the body.
	Tail bool
	// IsErr tints the bullet red.
	IsErr bool
	// Language, when non-empty, syntax-highlights each line with Chroma.
	Language string
	// Meta is a dim annotation appended to the header, e.g. "1.2s" or
	// "exit 1 · 12s".
	Meta string
}

// cardGutterWidth is the visible width of the "  │ " prefix each body row
// carries; body lines are clipped to Width minus this.
const cardGutterWidth = 4

// RenderOutputCard renders a tool's textual output as a card: a header row
// (● verb(target)) above the output, each line behind a "│" gutter, clipped
// and capped per opts. Empty output renders "(no output)". The trailing
// newline is omitted so callers control spacing (mirrors Card.Render).
func RenderOutputCard(verb, target, output string, opts OutputCardOpts) string {
	bullet := outBullet
	if opts.IsErr {
		bullet = outBulletErr
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s %s", bullet, renderCardHeader(verb, target, opts.Meta, opts.Width)))

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
	if opts.MaxLines > 0 && len(lines) > opts.MaxLines {
		extra = len(lines) - opts.MaxLines
		if opts.Tail {
			shown = lines[len(lines)-opts.MaxLines:]
		} else {
			shown = lines[:opts.MaxLines]
		}
	}

	if extra > 0 && opts.Tail {
		b.WriteString("\n  " + outMore.Render("… +"+pluralise(extra, "line")))
	}
	dark := IsDark()
	for _, ln := range shown {
		// Clip the raw line before highlighting: Chroma output is
		// self-contained (every token carries its own reset), whereas cutting
		// an already-highlighted string mid-token would strand an open escape.
		body := clipLine(expandTabs(ln), opts.Width-cardGutterWidth)
		if opts.Language != "" {
			body = highlightLine(body, opts.Language, dark)
		}
		b.WriteString("\n  " + outGutter.String() + " " + body)
	}
	if extra > 0 && !opts.Tail {
		b.WriteString("\n  " + outMore.Render("… +"+pluralise(extra, "line")))
	}
	return b.String()
}

// clipLine truncates s to at most limit terminal cells, ending in "…" when
// truncation occurred. limit <= 0 (unknown width) leaves s untouched. Wide
// (CJK) runes are measured by display width, not rune count.
func clipLine(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	return truncate.StringWithTail(s, uint(limit), "…")
}

// renderCardHeader renders "verb(target)" bold plus an optional dim "(meta)",
// clipping target so the whole header fits in width (2 cells are reserved for
// the caller's "● " bullet prefix).
func renderCardHeader(verb, target, meta string, width int) string {
	if width > 0 {
		avail := width - 2 - rw.StringWidth(verb) - 2 // bullet+space, parens
		if meta != "" {
			avail -= rw.StringWidth(meta) + 3 // " (" + ")"
		}
		// Floor at a few cells so a pathologically narrow terminal still gets
		// a clipped target instead of an unclipped, overflowing one.
		if avail < 3 {
			avail = 3
		}
		target = clipLine(target, avail)
	}
	h := headerVerb.Render(fmt.Sprintf("%s(%s)", verb, target))
	if meta != "" {
		h += " " + outMore.Render("("+meta+")")
	}
	return h
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
