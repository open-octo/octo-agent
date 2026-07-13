package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palette — adaptive so cards, panels, and chrome read on both light and dark
// terminals. Values follow GitHub's light/dark themes; lipgloss resolves the
// right side at render time from the terminal-background probe.
var (
	ColAccent  = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#3FB950"} // success / additions
	ColDanger  = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F85149"} // errors / removals
	ColWarning = lipgloss.AdaptiveColor{Light: "#D89614", Dark: "#FAAD14"} // partial / warnings
	ColMuted   = lipgloss.AdaptiveColor{Light: "#57606A", Dark: "#8B949E"} // secondary text
	ColDim     = lipgloss.AdaptiveColor{Light: "#6E7781", Dark: "#6E7681"} // gutters, line numbers
	ColDimmer  = lipgloss.AdaptiveColor{Light: "#AFB8C1", Dark: "#484F58"} // dimmest (unchanged line nos)
	ColBorder  = lipgloss.AdaptiveColor{Light: "#D0D7DE", Dark: "#30363D"} // panel / input-box border

	ColBrand    = lipgloss.AdaptiveColor{Light: "#6F42C1", Dark: "#A371F7"} // purple — octo brand
	ColBrandDim = lipgloss.AdaptiveColor{Light: "#B4A7D6", Dark: "#6E5494"} // muted purple
	ColUserMsg  = lipgloss.AdaptiveColor{Light: "#0969DA", Dark: "#58A6FF"} // blue — user messages
)

func IsDark() bool { return lipgloss.HasDarkBackground() }

var (
	panelBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColBorder).
			Padding(0, 1)
	panelTitle = lipgloss.NewStyle().Foreground(ColMuted).Bold(true)
)

// Panel renders body inside a rounded border with a styled title line on top.
// An empty body yields a title-only box. The trailing newline is omitted.
func Panel(title, body string) string {
	inner := panelTitle.Render(title)
	if body != "" {
		inner += "\n" + body
	}
	return panelBorder.Render(inner)
}

// Box wraps content in the same rounded border as Panel, with no title styling
// — for content that supplies its own heading (e.g. a modal prompt).
func Box(content string) string {
	return panelBorder.Render(content)
}

// ── Banner ─────────────────────────────────────────────────────────────────

// octoASCII is the giant pixel-art "OCTO" logotype rendered with block
// characters. It assumes a monospace font; proportionally spaced terminals will
// look misaligned, which is unavoidable for block-char ASCII art in a TUI.
var octoASCII = `
██████   ██████  ██████████   ██████
██    ██ ██    ██     ██      ██    ██
██    ██ ██           ██      ██    ██
██    ██ ██           ██      ██    ██
██    ██ ██           ██      ██    ██
██    ██ ██    ██     ██      ██    ██
 ██████   ██████      ██       ██████
`

// Banner renders the octo welcome header with a giant pixel-art "OCTO" mark.
// The logo sits left of the title and hints, Claude Code style.
func Banner(version, model, cwd string, width int) string {
	if width < 20 {
		width = 20
	}

	// Title: bold purple "◆ octo" with optional version
	titleStyle := lipgloss.NewStyle().Foreground(ColBrand).Bold(true)
	title := "◆ octo"
	if version != "" {
		title += "  " + version
	}

	// Path: muted model + cwd
	infoStyle := lipgloss.NewStyle().Foreground(ColMuted)
	info := model
	if cwd != "" {
		if info != "" {
			info += " · "
		}
		info += cwd
	}

	// Hints: dimmest, italic
	hintStyle := lipgloss.NewStyle().Foreground(ColDimmer).Italic(true)
	hints := []string{
		"Enter send · Shift+Enter/Ctrl+J newline · Ctrl+Q queue",
		"Shift+Tab perm-mode · Ctrl+T tasks · Esc interrupt · Ctrl+C quit",
	}

	// Build right-side info block vertically
	infoBlock := lipgloss.JoinVertical(lipgloss.Top,
		titleStyle.Render(title),
		infoStyle.Render(info),
		"", // blank line separator
		hintStyle.Render(hints[0]),
		hintStyle.Render(hints[1]),
	)

	// Horizontal join: art + 2-space gutter + info. Below ~75 columns this
	// join no longer fits the terminal width and lipgloss.JoinHorizontal
	// doesn't wrap — the result would wrap unpredictably wherever the
	// terminal itself line-wraps, misaligning the art and info block against
	// each other (#1095). Fall back to a text-only banner instead; the
	// width<20 floor above already keeps that block itself from being
	// squeezed further than makes sense.
	art := strings.TrimSpace(octoASCII)
	const gutter = 2
	var banner string
	if lipgloss.Width(art)+gutter+lipgloss.Width(infoBlock) <= width {
		banner = lipgloss.JoinHorizontal(lipgloss.Top, art, "  ", infoBlock)
	} else {
		banner = infoBlock
	}

	// Outer padding: 1 line top/bottom for breathing room
	return lipgloss.NewStyle().Padding(1, 0).Render(banner)
}

// ── Status bar ─────────────────────────────────────────────────────────────

var (
	statusSegStyle   = lipgloss.NewStyle().Foreground(ColMuted)
	statusHintStyle  = lipgloss.NewStyle().Foreground(ColDimmer).Italic(true)
	statusBarStyle   = lipgloss.NewStyle().Foreground(ColBorder)
	statusModelStyle = lipgloss.NewStyle().Foreground(ColBrandDim).Bold(true)
	statusTimeStyle  = lipgloss.NewStyle().Foreground(ColMuted)
	statusShellStyle = lipgloss.NewStyle().Foreground(ColAccent)
)

// StatusBar renders the bottom status line with styled segments.
// Each segment is a [label, value] pair; the hint is shown on a second line.
func StatusBar(segments [][2]string, hint string, width int) string {
	segments = clampSegmentsToWidth(segments, width)
	var b strings.Builder
	if width > 3 {
		b.WriteString(statusBarStyle.Render(strings.Repeat("─", width)))
		b.WriteByte('\n')
	}
	for i, seg := range segments {
		if i > 0 {
			b.WriteString(statusSegStyle.Render(" · "))
		}
		label, value := seg[0], seg[1]
		var rendered string
		switch label {
		case "model":
			rendered = statusModelStyle.Render(value)
		case "elapsed":
			rendered = statusTimeStyle.Render(value)
		case "shell":
			rendered = statusShellStyle.Render(value)
		default:
			rendered = statusSegStyle.Render(value)
		}
		b.WriteString(rendered)
	}
	if hint != "" {
		b.WriteByte('\n')
		b.WriteString(statusHintStyle.Render(hint))
	}
	return b.String()
}

// clampSegmentsToWidth shortens the "cwd" segment's value — the one whose
// length varies most and is the most tolerant of abbreviation — so the
// segment line fits within width instead of silently wrapping onto a second
// line on a deep working directory (#1095). Other segments (model, ctx%,
// perm, ...) are short, fixed-shape labels and are left alone.
func clampSegmentsToWidth(segments [][2]string, width int) [][2]string {
	if width <= 0 {
		return segments
	}
	over := segmentsWidth(segments) - width
	if over <= 0 {
		return segments
	}
	out := make([][2]string, len(segments))
	copy(out, segments)
	for i, seg := range out {
		if seg[0] != "cwd" {
			continue
		}
		budget := lipgloss.Width(seg[1]) - over
		if budget < 1 {
			budget = 1
		}
		out[i] = [2]string{seg[0], shortenMiddle(seg[1], budget)}
		break
	}
	return out
}

// segmentsWidth measures the plain display width the segment line will
// render at: each segment's value (labels aren't rendered, only used to pick
// a style) plus a " · " separator between segments.
func segmentsWidth(segments [][2]string) int {
	w := 0
	for i, seg := range segments {
		if i > 0 {
			w += 3 // " · "
		}
		w += lipgloss.Width(seg[1])
	}
	return w
}

// shortenMiddle shortens s to at most maxW display columns by cutting from
// the middle and inserting "…", keeping both the start and the end visible
// (e.g. "~/Projects/github/octo-agent" → "~/Pro…/octo-agent") — the leaf
// directory name is usually the most useful part of a path, and padCol's
// tail-truncation would hide it entirely.
func shortenMiddle(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxW {
		return s
	}
	if maxW == 1 {
		return "…"
	}
	r := []rune(s)
	avail := maxW - 1 // reserve 1 column for "…"
	left := avail / 2
	right := avail - left
	if left+right >= len(r) {
		return s
	}
	return string(r[:left]) + "…" + string(r[len(r)-right:])
}
