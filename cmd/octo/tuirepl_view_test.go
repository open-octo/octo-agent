package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestSoftWrappedRows_LongLineWraps(t *testing.T) {
	m := newTestModel()
	m.ta.SetWidth(20)
	setInput(m, strings.Repeat("a", 40))

	got := m.softWrappedRows()
	if got < 2 {
		t.Errorf("long line should soft-wrap into multiple rows, got %d", got)
	}
}

func TestSoftWrappedRows_HardNewlinesCount(t *testing.T) {
	m := newTestModel()
	m.ta.SetWidth(80)
	setInput(m, "one\ntwo\nthree")

	if got := m.softWrappedRows(); got != 3 {
		t.Errorf("three hard newlines = 3 rows, got %d", got)
	}
}

// The live frame must never fill the terminal's last cell: a full-width line
// leaves the terminal in wrap-pending state, and any line whose width the
// renderer and the terminal disagree on (CJK, emoji, ambiguous-width glyphs)
// physically wraps while counting as one — the next repaint undershoots its
// cursor-up and the old frame's top line (idle: the input box) is never
// overwritten, leaving a stuck duplicate on screen.
func TestTUI_FrameCapsLinesBelowTerminalWidth(t *testing.T) {
	m := newTestModel()
	m.width = 40
	out := m.frame(strings.Repeat("很", 60) + "\nshort")
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > m.width-1 {
			t.Errorf("line width = %d, want ≤ %d (one cell of slack): %q", w, m.width-1, line)
		}
	}
	// Absurdly narrow terminals skip the cap instead of truncating to nothing.
	m.width = 1
	in := strings.Repeat("x", 10)
	if got := m.frame(in); got != in {
		t.Error("width < 2 should leave the frame untouched")
	}
}

// The textarea wraps at the same boundary the frame cap enforces, so typed
// text is never visibly truncated by one cell. bubbles reports the text
// width excluding the 2-cell "> " prompt, so 80 − 1 (slack) − 2 (prompt) = 77.
func TestTUI_TextAreaWidthLeavesOneCellSlack(t *testing.T) {
	m := newTestModel()
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if got := m.ta.Width(); got != 77 {
		t.Errorf("ta.Width = %d, want 77 (terminal width − 1 cell slack − 2 cell prompt)", got)
	}
}

// End to end: the composed View — status-bar separator included — never lets a
// line reach the terminal's last cell. This pins the cap at the View level so
// a future return path can't silently bypass frame(). The status separator
// alone ("─" × width) would fill the last cell without it.
func TestTUI_ViewLinesNeverFillTheLastCell(t *testing.T) {
	m := newTestModel()
	m.width = 40
	for _, line := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(line); w > m.width-1 {
			t.Errorf("View line width = %d, want ≤ %d: %q", w, m.width-1, line)
		}
	}
}
