package main

import (
	"fmt"
	"strings"
)

// complItem is one row in the slash-command completion menu.
type complItem struct {
	name string // the trigger, e.g. "/skills" or "/review"
	desc string // one-line description shown to the right
}

// maxComplVisible caps how many completion rows render at once; the list
// scrolls around the selection beyond that.
const maxComplVisible = 10

// builtinSlashCommands are the always-available TUI slash commands, in display
// order. Skills are appended after these by slashCandidates.
var builtinSlashCommands = []complItem{
	{"/help", "List commands and tools"},
	{"/model", "Switch to another model (--default to make it the default)"},
	{"/thinking", "Set reasoning effort (off/low/medium/high/xhigh/max)"},
	{"/compact", "Summarize older history to free up context"},
	{"/transcript", "Re-print the last (or last N) tool call(s) with full output"},
	{"/goal", "Set or view the session goal (edit/pause/resume/clear)"},
	{"/loop", "Repeat a task in this session (/loop [interval] <task>)"},
	{"/clear", "Wipe the conversation and start fresh"},
	{"/skills", "List discovered skills"},
	{"/mcp", "List connected MCP servers"},
	{"/workflows", "List available workflows"},
	{"/memory", "Show cross-session memory"},
	{"/init", "Generate a .octorules guide for this repo"},
	{"/save", "Save the current session"},
	{"/sessions", "List recent sessions"},
	{"/trash", "Recover files the agent deleted or overwrote"},
	{"/exit", "Quit octo"},
}

// slashCandidates returns every completable slash command: the built-ins plus
// one "/<name>" entry per discovered skill.
func (m *tuiModel) slashCandidates() []complItem {
	items := make([]complItem, 0, len(builtinSlashCommands)+16)
	items = append(items, builtinSlashCommands...)
	if m.cfg.skillReg != nil {
		for _, s := range m.cfg.skillReg.List() {
			items = append(items, complItem{name: "/" + s.Name, desc: s.Description})
		}
	}
	return items
}

// updateCompletion (re)computes the menu from the current input. It opens the
// menu when the input is a bare "/command" token (a leading slash, no
// whitespace yet); otherwise it closes the menu. The menu is available while a
// turn runs so slash commands can be queued and executed once it finishes.
// complIdx is clamped but not reset here — callers reset it on a fresh edit.
func (m *tuiModel) updateCompletion() {
	v := m.ta.Value()
	if !strings.HasPrefix(v, "/") || strings.ContainsAny(v, " \t\n") {
		m.complItems = nil
		return
	}
	prefix := strings.ToLower(v)
	var matches []complItem
	for _, c := range m.slashCandidates() {
		if strings.HasPrefix(strings.ToLower(c.name), prefix) {
			matches = append(matches, c)
		}
	}
	m.complItems = matches
	if m.complIdx >= len(matches) {
		m.complIdx = 0
	}
}

// acceptCompletion fills the input with the highlighted command plus a trailing
// space and closes the menu. Tab calls it alone (fill, then type args); Enter
// calls it and submits in the same keystroke (run immediately).
func (m *tuiModel) acceptCompletion() {
	if m.complIdx < 0 || m.complIdx >= len(m.complItems) {
		return
	}
	m.ta.SetValue(m.complItems[m.complIdx].name + " ")
	m.ta.CursorEnd()
	m.complItems = nil
}

// complWindow returns the [start,end) slice of complItems to render, scrolled so
// the selection stays visible.
func (m *tuiModel) complWindow() (start, end int) {
	n := len(m.complItems)
	if n <= maxComplVisible {
		return 0, n
	}
	start = m.complIdx - maxComplVisible/2
	if start < 0 {
		start = 0
	}
	if start > n-maxComplVisible {
		start = n - maxComplVisible
	}
	return start, start + maxComplVisible
}

// completionView renders the menu: one row per candidate (name + description),
// the selection marked, and a key-hint footer.
func (m *tuiModel) completionView() string {
	if len(m.complItems) == 0 {
		return ""
	}
	start, end := m.complWindow()
	var b strings.Builder
	for i := start; i < end; i++ {
		it := m.complItems[i]
		name := fmt.Sprintf("%-20s", it.name)
		if i == m.complIdx {
			b.WriteString(complSelStyle.Render("▸ "+name) + " " + hintStyle.Render(truncate1Line(it.desc)))
		} else {
			b.WriteString("  " + complNameStyle.Render(name) + " " + hintStyle.Render(truncate1Line(it.desc)))
		}
		b.WriteByte('\n')
	}
	hint := "↑↓ select · Enter run · Tab complete · Esc dismiss"
	if n := len(m.complItems); n > maxComplVisible {
		hint = fmt.Sprintf("…%d more · %s", n-maxComplVisible, hint)
	}
	b.WriteString(hintStyle.Render("  " + hint))
	b.WriteByte('\n')
	return b.String()
}
