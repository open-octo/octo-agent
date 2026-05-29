package tui

import "github.com/charmbracelet/lipgloss"

// Palette — adaptive so cards, panels, and chrome read on both light and dark
// terminals. Values follow GitHub's light/dark themes; lipgloss resolves the
// right side at render time from the terminal-background probe.
var (
	ColAccent = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#3FB950"} // success / additions
	ColDanger = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F85149"} // errors / removals
	ColMuted  = lipgloss.AdaptiveColor{Light: "#57606A", Dark: "#8B949E"} // secondary text
	ColDim    = lipgloss.AdaptiveColor{Light: "#6E7781", Dark: "#6E7681"} // gutters, line numbers
	ColDimmer = lipgloss.AdaptiveColor{Light: "#AFB8C1", Dark: "#484F58"} // dimmest (unchanged line nos)
	ColBorder = lipgloss.AdaptiveColor{Light: "#D0D7DE", Dark: "#30363D"} // panel / input-box border
)

// IsDark reports whether the terminal has a dark background. The raw-ANSI diff
// row washes and the Chroma style can't go through lipgloss.AdaptiveColor, so
// they pick light/dark via this. Cached by lipgloss after the first probe.
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
