package main

import (
	"io"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/open-octo/octo-agent/internal/tui"
)

// selectItem is one row in an arrow-key selection menu. label is the primary
// text, desc the dimmed annotation shown after it (e.g. "default", a base URL,
// a vendor id), and value the string returned when the row is chosen.
type selectItem struct {
	label string
	desc  string
	value string
}

var (
	selectCursorStyle = lipgloss.NewStyle().Foreground(tui.ColBrand).Bold(true)
	selectLabelStyle  = lipgloss.NewStyle().Foreground(tui.ColBrand).Bold(true)
	selectDescStyle   = lipgloss.NewStyle().Foreground(tui.ColMuted)
	selectPromptStyle = lipgloss.NewStyle().Bold(true)
)

// selectModel is a minimal single-select list: arrow keys (or j/k) move the
// cursor, Enter chooses, Esc/Ctrl-C cancels. It renders inline (no alt-screen)
// and clears itself on exit so the wizard can print a one-line confirmation.
type selectModel struct {
	prompt    string
	items     []selectItem
	cursor    int
	chosen    bool
	cancelled bool
}

func (m selectModel) Init() tea.Cmd { return nil }

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch {
	case key.Matches(km, selectKeys.up):
		if m.cursor > 0 {
			m.cursor--
		}
	case key.Matches(km, selectKeys.down):
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case key.Matches(km, selectKeys.choose):
		m.chosen = true
		return m, tea.Quit
	case key.Matches(km, selectKeys.cancel):
		m.cancelled = true
		return m, tea.Quit
	}
	return m, nil
}

func (m selectModel) View() string {
	// Once the user has acted, render nothing — the menu disappears and the
	// wizard prints the resolved choice in its place.
	if m.chosen || m.cancelled {
		return ""
	}
	var b []byte
	b = append(b, selectPromptStyle.Render(m.prompt)...)
	b = append(b, '\n')
	for i, it := range m.items {
		cursor := "  "
		label := it.label
		if i == m.cursor {
			cursor = selectCursorStyle.Render("❯ ")
			label = selectLabelStyle.Render(label)
		}
		b = append(b, "  "...)
		b = append(b, cursor...)
		b = append(b, label...)
		if it.desc != "" {
			b = append(b, "  "...)
			b = append(b, selectDescStyle.Render(it.desc)...)
		}
		b = append(b, '\n')
	}
	b = append(b, selectDescStyle.Render("  ↑/↓ move · Enter select · Esc cancel")...)
	b = append(b, '\n')
	return string(b)
}

type selectKeyMap struct {
	up     key.Binding
	down   key.Binding
	choose key.Binding
	cancel key.Binding
}

var selectKeys = selectKeyMap{
	up:     key.NewBinding(key.WithKeys("up", "k")),
	down:   key.NewBinding(key.WithKeys("down", "j")),
	choose: key.NewBinding(key.WithKeys("enter")),
	cancel: key.NewBinding(key.WithKeys("esc", "ctrl+c")),
}

// runSelect shows an arrow-key menu over items and returns the chosen item.
// The cursor starts on the row whose value equals defaultValue (else the
// first). ok is false if the user cancelled (Esc/Ctrl-C) or the program
// errored — callers treat that as "abort the wizard".
func runSelect(in io.Reader, out io.Writer, prompt string, items []selectItem, defaultValue string) (selectItem, bool) {
	start := 0
	for i, it := range items {
		if it.value == defaultValue {
			start = i
			break
		}
	}
	m := selectModel{prompt: prompt, items: items, cursor: start}
	final, err := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out)).Run()
	if err != nil {
		return selectItem{}, false
	}
	res := final.(selectModel)
	if !res.chosen || res.cancelled {
		return selectItem{}, false
	}
	return res.items[res.cursor], true
}
