package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"
	"unsafe"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	rw "github.com/mattn/go-runewidth"
	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/permission"
	"github.com/open-octo/octo-agent/internal/tools"
	"github.com/open-octo/octo-agent/internal/tui"
	"github.com/rivo/uniseg"
)

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var (
	promptStyle          = lipgloss.NewStyle().Foreground(tui.ColBrand).Bold(true)
	noticeStyle          = lipgloss.NewStyle().Foreground(tui.ColMuted)
	errorStyle           = lipgloss.NewStyle().Foreground(tui.ColDanger)
	toolErrStyle         = lipgloss.NewStyle().Foreground(tui.ColDanger)
	queueStyle           = lipgloss.NewStyle().Foreground(tui.ColAccent)
	modalStyle           = lipgloss.NewStyle().Foreground(tui.ColBrand).Bold(true)
	hintStyle            = lipgloss.NewStyle().Foreground(tui.ColDimmer).Italic(true)
	activityStyle        = lipgloss.NewStyle().Foreground(tui.ColBrand)
	userEchoStyle        = lipgloss.NewStyle().Foreground(tui.ColUserMsg).Bold(true)
	assistantPrefixStyle = lipgloss.NewStyle().Foreground(tui.ColBrand).Bold(true)
	pendingSteerStyle    = lipgloss.NewStyle().Foreground(tui.ColMuted)
	complSelStyle        = lipgloss.NewStyle().Foreground(tui.ColBrand).Bold(true)
	complNameStyle       = lipgloss.NewStyle().Foreground(tui.ColAccent)
	bgDoneStyle          = lipgloss.NewStyle().Foreground(tui.ColAccent)
)

func (m *tuiModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.modal != nil {
		return m.handleModalKey(msg)
	}

	// Sub-agent panel navigation mode takes over ↑/↓/Enter/Esc when active.
	if m.subAgentFocus >= 0 {
		return m.handleSubAgentPanelKey(msg)
	}

	// Slash-command completion menu owns Tab/↑/↓/Enter/Esc while it's open, so
	// it can navigate and accept without those keys reaching history nav or
	// submit. Plain typing falls through and re-filters the menu below.
	// Key semantics mirror Claude Code: Enter runs the highlighted command
	// immediately; Tab fills it into the input to add arguments first.
	if len(m.complItems) > 0 {
		switch msg.Type {
		case tea.KeyDown:
			m.complIdx = (m.complIdx + 1) % len(m.complItems)
			return m, nil
		case tea.KeyUp:
			m.complIdx = (m.complIdx - 1 + len(m.complItems)) % len(m.complItems)
			return m, nil
		case tea.KeyTab:
			m.acceptCompletion()
			return m, m.updateTextAreaHeight()
		case tea.KeyEnter:
			if !msg.Alt {
				m.acceptCompletion()
				return m.submit()
			}
		case tea.KeyEsc:
			m.complItems = nil
			return m, nil
		}
	}

	// Ghost-text follow-up: Tab or → accepts the pending suggestion when the
	// input is empty, filling it in to edit or send. With text present these
	// keys keep their normal behaviour (cursor / tab insert).
	if m.suggestion != "" && strings.TrimSpace(m.ta.Value()) == "" {
		if msg.Type == tea.KeyTab || msg.Type == tea.KeyRight {
			m.acceptSuggestion()
			return m, m.updateTextAreaHeight()
		}
	}

	// A large bracketed paste is collapsed into an inline "[#N pasted …]" token
	// so the box stays readable; the full text is restored on submit/queue.
	// Small pastes fall through and insert verbatim so short snippets stay
	// visible and editable.
	if msg.Paste && shouldFoldPaste(string(msg.Runes)) {
		return m.insertPasteToken(string(msg.Runes))
	}

	switch msg.Type {
	case tea.KeyCtrlD:
		m.quit = true
		return m, tea.Quit

	case tea.KeyCtrlB:
		// Background the current sync terminal or sub-agent, if one is running.
		// No-op when no sync task is polling.
		if m.turnRunning {
			if tools.HasActiveSync() {
				tools.PromoteCurrentSync()
			} else if tools.HasActiveSubAgentSync() {
				tools.PromoteCurrentSubAgentSync()
			}
		}
		return m, nil

	case tea.KeyCtrlC:
		if m.turnRunning {
			m.interrupt()
			return m, nil
		}
		m.quit = true
		return m, tea.Quit

	case tea.KeyEsc:
		if m.turnRunning {
			// Take-back: Esc before the model has produced any output (the echo
			// is still pending in the live area). Drop the not-yet-committed echo
			// and restore the typed text to the input box for editing — the agent's
			// interrupt rolls the unanswered user message back out of history, so
			// it leaves no trace in the scrollback. Once output has streamed the
			// echo is already committed (echoPending == "") and Esc interrupts +
			// recalls the last submitted message from history.
			if m.echoPending != "" {
				restore := m.echoRestore
				m.echoPending = ""
				m.echoRestore = ""
				m.interrupt()
				if restore != "" {
					m.ta.SetValue(restore)
					m.ta.CursorEnd()
					m.inputHistoryIdx = -1
					return m, m.updateTextAreaHeight()
				}
				return m, nil
			}
			// After output has streamed the echo is committed; Esc interrupts
			// and recalls the last submitted message into the input box so the
			// user can edit and resubmit without pressing ↑ manually.
			//
			// Pending steers are exempt: they stay in the inbox and run as the
			// next turn on their own (handleTurnFinished drains it), so recalling
			// them here would both run the steer AND strand its text in the box.
			// Note interrupt() clears pendingSteer, so capture it first.
			hadSteer := len(m.pendingSteer) > 0
			m.interrupt()
			if hadSteer {
				return m, nil
			}
			if n := len(m.inputHistory); n > 0 && m.ta.Value() == "" {
				m.ta.SetValue(m.inputHistory[n-1])
				m.ta.CursorEnd()
				m.inputHistoryIdx = -1
				return m, m.updateTextAreaHeight()
			}
			return m, nil
		}
		// Idle: clear the input line and discard any pending attachments.
		// A pending /goal edit is cancelled too — otherwise the next
		// unrelated submit would silently become the objective.
		if m.goalEditPending {
			m.goalEditPending = false
			m.println(noticeStyle.Render("Goal edit cancelled"))
		}
		m.pendingAttachments = nil
		m.clearPastes()
		// Also clear folded state
		if m.inputFolded {
			m.inputFolded = false
			m.foldedFullText = ""
			m.inputFoldedLines = 0
		}
		m.ta.Reset()
		m.inputHistoryIdx = -1
		return m, m.updateTextAreaHeight()

	case tea.KeyCtrlJ:
		// Ctrl+J inserts a newline (LF) into the textarea. Works on all
		// terminals, including those where Alt+Enter is not mapped.
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if hcmd := m.updateTextAreaHeight(); hcmd != nil {
			cmd = tea.Batch(cmd, hcmd)
		}
		return m, cmd

	case tea.KeyCtrlQ:
		// Queue the current input to run as a future turn. Idle, there is no
		// turn to wait for and nothing ever drains the queue — the design
		// table (dev-docs/tui-input-modes-design.md §7) makes the queue key
		// behave exactly like Enter, so delegate to the same submit path.
		if !m.turnRunning {
			return m.submit()
		}
		raw := m.ta.Value()
		if m.inputFolded {
			raw = m.foldedFullText
		}
		text := strings.TrimSpace(m.expandPastes(raw))
		collapsed := strings.TrimSpace(raw)
		if text == "" {
			return m, nil
		}
		m.ta.Reset()
		m.inputHistoryIdx = -1
		m.clearPastes()
		// Clear folded state
		if m.inputFolded {
			m.inputFolded = false
			m.foldedFullText = ""
			m.inputFoldedLines = 0
		}
		if len(m.inputHistory) == 0 || m.inputHistory[len(m.inputHistory)-1] != text {
			m.inputHistory = append(m.inputHistory, text)
		}
		m.queue = append(m.queue, pendingItem{text: text})
		m.println(queueStyle.Render("＋ queued: " + collapsed))
		return m, nil

	case tea.KeyCtrlX:
		// Cancel the most-recently queued message. The queue survives Esc
		// (interrupt only stops the running turn), so this is the way to undo a
		// mis-queued Ctrl+Q; repeat to clear the queue.
		if n := len(m.queue); n > 0 {
			dropped := m.queue[n-1].text
			m.queue = m.queue[:n-1]
			m.println(queueStyle.Render("✕ unqueued: " + dropped))
		}
		return m, nil

	case tea.KeyCtrlV:
		// Paste an image from the clipboard as an attachment on the next turn.
		// Normal text paste (Cmd+V on macOS) arrives as bracketed-paste runes
		// handled by the textarea, so this binding is free for image paste.
		return m.pasteClipboardImage()

	case tea.KeyCtrlT:
		// Toggle the pinned task checklist (Claude Code's ctrl+t). While a turn
		// runs the list shows anyway; the pin keeps it visible when idle and
		// includes a fully-completed list.
		m.showTasks = !m.showTasks
		return m, nil

	case tea.KeyShiftTab:
		// Cycle permission mode: interactive → auto → strict → interactive.
		// Previously only interactive/auto were reachable from the keyboard,
		// leaving strict mode unreachable without a restart (#1097).
		if m.cfg.permEngine != nil {
			var next permission.Mode
			switch m.cfg.permEngine.GetMode() {
			case permission.ModeInteractive:
				next = permission.ModeAutoApprove
			case permission.ModeAutoApprove:
				next = permission.ModeStrict
			default:
				next = permission.ModeInteractive
			}
			m.cfg.permEngine.SetMode(next)
			m.println(noticeStyle.Render("Permission mode: " + string(next)))
		}
		return m, nil

	case tea.KeyEnter:
		if msg.Alt {
			// Alt+Enter inserts a newline into the textarea.
			var cmd tea.Cmd
			m.ta, cmd = m.ta.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if hcmd := m.updateTextAreaHeight(); hcmd != nil {
				cmd = tea.Batch(cmd, hcmd)
			}
			return m, cmd
		}
		return m.submit()

	case tea.KeyUp:
		// Retract the most recent pending steer (typed mid-turn, not yet drained)
		// back into the empty input box for editing — and drop it from the queue
		// so it won't also be sent. If it was already drained (Inbox.Remove
		// fails) it's committed; fall through to ordinary history recall.
		if strings.TrimSpace(m.ta.Value()) == "" && len(m.pendingSteer) > 0 {
			last := m.pendingSteer[len(m.pendingSteer)-1]
			if m.a.Inbox.Remove(last) {
				m.pendingSteer = m.pendingSteer[:len(m.pendingSteer)-1]
				m.ta.SetValue(last)
				m.ta.CursorEnd()
				return m, m.updateTextAreaHeight()
			}
		}
		// If the cursor is not on the first display row, move up inside the
		// textarea (line navigation). Otherwise browse input history.
		if m.ta.Line() > 0 || m.ta.LineInfo().RowOffset > 0 {
			m.ta.CursorUp()
			return m, nil
		}
		if m.inputHistoryIdx+1 < len(m.inputHistory) {
			if m.inputHistoryIdx == -1 {
				m.inputDraft = m.ta.Value()
			}
			m.inputHistoryIdx++
			m.ta.SetValue(m.inputHistory[len(m.inputHistory)-1-m.inputHistoryIdx])
			m.ta.CursorEnd()
			return m, m.updateTextAreaHeight()
		}
		// History exhausted and input empty — shift focus to the sub-agent panel
		// when there are running agents.
		if strings.TrimSpace(m.ta.Value()) == "" && len(m.subAgentOrder) > 0 {
			m.subAgentFocus = len(m.subAgentOrder) - 1
			return m, nil
		}
		return m, nil

	case tea.KeyDown:
		// If the cursor is not on the last display row, move down inside the
		// textarea (line navigation). Otherwise browse input history.
		li := m.ta.LineInfo()
		lines := strings.Count(m.ta.Value(), "\n") + 1
		if m.ta.Line() < lines-1 || li.RowOffset < li.Height-1 {
			m.ta.CursorDown()
			return m, nil
		}
		if m.inputHistoryIdx > 0 {
			m.inputHistoryIdx--
			m.ta.SetValue(m.inputHistory[len(m.inputHistory)-1-m.inputHistoryIdx])
			m.ta.CursorEnd()
			return m, m.updateTextAreaHeight()
		} else if m.inputHistoryIdx == 0 {
			m.inputHistoryIdx = -1
			m.ta.SetValue(m.inputDraft)
			m.inputDraft = ""
			m.ta.CursorEnd()
			return m, m.updateTextAreaHeight()
		}
		return m, nil
	}

	// Folded state toggle: Tab expands/collapses when there's multi-line content.
	// Checked before the textarea ever sees the keypress — handing Tab to the
	// textarea first inserts a literal tab into its value, which then leaked
	// into foldedFullText on collapse (#1097). Tab is only captured when the
	// completion menu is not open (a non-empty menu already returned above).
	if msg.Type == tea.KeyTab && len(m.complItems) == 0 {
		if m.inputFolded {
			// Expand: restore the full text
			m.ta.SetValue(m.foldedFullText)
			m.inputFolded = false
			m.foldedFullText = ""
			return m, m.updateTextAreaHeight()
		}
		// Collapse: fold if there are many lines
		if lines := strings.Count(m.ta.Value(), "\n") + 1; lines >= 5 {
			m.foldedFullText = m.ta.Value()
			m.inputFolded = true
			m.inputFoldedLines = lines
			return m, nil
		}
	}

	// Everything else (typing, left/right, backspace, word movement, etc.)
	// is handled by bubbles/textarea.
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)

	// Detect a dropped image file path (some terminals paste the path as text
	// when a file is dragged in). If the input box now holds a single image
	// file path, swallow it and queue it as an attachment instead.
	if m.tryAttachDroppedImage() {
		return m, m.updateTextAreaHeight()
	}
	// A fresh edit re-filters the slash-completion menu from the top.
	m.complIdx = 0
	m.updateCompletion()
	if hcmd := m.updateTextAreaHeight(); hcmd != nil {
		cmd = tea.Batch(cmd, hcmd)
	}
	return m, cmd
}

// handleSubAgentPanelKey routes keys when the sub-agent panel has focus.
// ↑/↓ moves between agents, Enter toggles expand/collapse, Esc returns to input.
func (m *tuiModel) handleSubAgentPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp:
		if m.subAgentFocus > 0 {
			m.subAgentFocus--
		}
		return m, nil
	case tea.KeyDown:
		if m.subAgentFocus < len(m.subAgentOrder)-1 {
			m.subAgentFocus++
		} else {
			// ↓ past the last agent returns focus to the input box.
			m.subAgentFocus = -1
		}
		return m, nil
	case tea.KeyEnter:
		if m.subAgentFocus >= 0 && m.subAgentFocus < len(m.subAgentOrder) {
			id := m.subAgentOrder[m.subAgentFocus]
			if sa := m.subAgents[id]; sa != nil {
				sa.expanded = !sa.expanded
			}
		}
		return m, nil
	case tea.KeyEsc:
		m.subAgentFocus = -1
		return m, nil
	}
	return m, nil
}

// pastedBlock is one large bracketed paste captured as an inline placeholder
// token. placeholder is the exact text inserted into the textarea (e.g.
// "[#1 pasted 123 lines]"); content is the full pasted text restored verbatim
// on submit/queue.
type pastedBlock struct {
	placeholder string
	content     string
}

const (
	// pasteFoldMinLines / pasteFoldMinChars: a bracketed paste at least this big
	// is collapsed into a placeholder token instead of being dumped into the box.
	// The char threshold catches a single huge line (one long paragraph) that
	// the line threshold alone would miss.
	pasteFoldMinLines = 5
	pasteFoldMinChars = 400
)

// shouldFoldPaste reports whether a pasted string is large enough to collapse
// into a placeholder token rather than insert verbatim.
func shouldFoldPaste(s string) bool {
	if strings.Count(s, "\n")+1 >= pasteFoldMinLines {
		return true
	}
	return len([]rune(s)) >= pasteFoldMinChars
}

// insertPasteToken records content as a pasted block and inserts a compact
// "[#N pasted …]" placeholder at the cursor in place of the raw text.
func (m *tuiModel) insertPasteToken(content string) (tea.Model, tea.Cmd) {
	m.pasteSeq++
	var label string
	if lines := strings.Count(content, "\n") + 1; lines >= 2 {
		label = fmt.Sprintf("[#%d pasted %d lines]", m.pasteSeq, lines)
	} else {
		label = fmt.Sprintf("[#%d pasted %d chars]", m.pasteSeq, len([]rune(content)))
	}
	m.pastedBlocks = append(m.pastedBlocks, pastedBlock{placeholder: label, content: content})
	// Insert the token at the cursor exactly as if the label had been typed.
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(label)})
	m.complIdx = 0
	m.updateCompletion()
	if hcmd := m.updateTextAreaHeight(); hcmd != nil {
		cmd = tea.Batch(cmd, hcmd)
	}
	return m, cmd
}

// expandPastes replaces each placeholder token in text with its full pasted
// content. A no-op when no pastes are pending or the tokens were edited away.
func (m *tuiModel) expandPastes(text string) string {
	for _, p := range m.pastedBlocks {
		text = strings.ReplaceAll(text, p.placeholder, p.content)
	}
	return text
}

// clearPastes drops all pending pasted blocks and resets token numbering. Call
// it whenever the input box content is consumed or discarded.
func (m *tuiModel) clearPastes() {
	m.pastedBlocks = nil
	m.pasteSeq = 0
}

// imageExts are the file extensions tryAttachDroppedImage recognises.
var imageExts = []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tiff", ".tif", ".heic", ".ico"}

// tryAttachDroppedImage checks whether the textarea currently contains an
// image file path (some terminals paste a dragged file's path as text).  The
// path may be surrounded by ordinary text, e.g. "look at /path/to/img.png and
// tell me", may be backslash-escaped (terminal drag escapes spaces and parens),
// or wrapped in quotes.  If a valid image file path is found, it is removed
// from the textarea, queued as a pending attachment, and true is returned.
func (m *tuiModel) tryAttachDroppedImage() bool {
	full := m.ta.Value()
	path, start, end, ok := findImagePath(full)
	if !ok {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	ext := strings.ToLower(filepath.Ext(path))
	mime := "image/" + strings.TrimPrefix(ext, ".")
	switch ext {
	case ".jpg":
		mime = "image/jpeg"
	case ".tif":
		mime = "image/tiff"
	}
	label := fmt.Sprintf("image (%s, %s)", shortMIME(mime), humanByteSize(len(data)))
	m.pendingAttachments = append(m.pendingAttachments, pendingAttachment{
		block: agent.NewImageBlock(mime, data),
		label: label,
	})
	// Splice the path token out of the (original) textarea value.
	before := strings.TrimSpace(full[:start])
	after := strings.TrimSpace(full[end:])
	if before != "" && after != "" {
		m.ta.SetValue(before + " " + after)
	} else {
		m.ta.SetValue(before + after)
	}
	m.ta.CursorEnd()
	return true
}

// findImagePath scans s for a token that resolves to an existing image file. It
// operates on the raw string (no early unescaping, so a backslash-escaped space
// is not mistaken for a token boundary) and returns the cleaned filesystem path
// plus the [start,end) byte range it occupies in s, so the caller can splice it
// out. It first tries the whole trimmed input — the common case where a file is
// dropped into an otherwise-empty box — which also catches quoted paths whose
// internal spaces aren't backslash-escaped, then falls back to scanning for a
// path embedded in surrounding text.
func findImagePath(s string) (path string, start, end int, ok bool) {
	if trimmed := strings.TrimSpace(s); trimmed != "" {
		if clean := cleanDroppedPath(trimmed); hasImageExt(clean) {
			if info, err := os.Stat(clean); err == nil && !info.IsDir() {
				st := strings.Index(s, trimmed)
				return clean, st, st + len(trimmed), true
			}
		}
	}

	lower := strings.ToLower(s)
	for _, ext := range imageExts {
		for from := 0; ; {
			rel := strings.Index(lower[from:], ext)
			if rel == -1 {
				break
			}
			idx := from + rel
			from = idx + len(ext)

			e := idx + len(ext)
			for e < len(s) && !isUnescapedBoundary(s, e) {
				e++
			}
			st := idx
			for st > 0 && !isUnescapedBoundary(s, st-1) {
				st--
			}
			clean := cleanDroppedPath(s[st:e])
			if !hasImageExt(clean) {
				continue
			}
			if info, err := os.Stat(clean); err == nil && !info.IsDir() {
				return clean, st, e, true
			}
		}
	}
	return "", 0, 0, false
}

// isUnescapedBoundary reports whether s[i] is whitespace that delimits a path
// token. On POSIX a backslash escapes the following space ("foo\ bar" is one
// token), so a space preceded by an odd number of backslashes is not a
// boundary; on Windows the backslash is the path separator, not an escape, so
// every space is a boundary (spaced paths arrive quoted and are handled by the
// whole-input fast path instead).
func isUnescapedBoundary(s string, i int) bool {
	if c := s[i]; c != ' ' && c != '\t' && c != '\n' {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	bs := 0
	for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
		bs++
	}
	return bs%2 == 0
}

// cleanDroppedPath strips one layer of matching surrounding quotes and, on
// POSIX, removes backslash escapes, turning a terminal-dropped token into a real
// filesystem path. On Windows backslashes are path separators (spaces are
// quoted, not escaped), so they are left intact.
func cleanDroppedPath(raw string) string {
	p := strings.TrimSpace(raw)
	if len(p) >= 2 {
		if (p[0] == '\'' && p[len(p)-1] == '\'') || (p[0] == '"' && p[len(p)-1] == '"') {
			p = p[1 : len(p)-1]
		}
	}
	if runtime.GOOS == "windows" {
		return p
	}
	var b strings.Builder
	b.Grow(len(p))
	for i := 0; i < len(p); i++ {
		if p[i] == '\\' && i+1 < len(p) {
			i++
		}
		b.WriteByte(p[i])
	}
	return b.String()
}

// hasImageExt reports whether path ends with a recognised image extension.
func hasImageExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range imageExts {
		if ext == e {
			return true
		}
	}
	return false
}

// pasteClipboardImage captures an image from the system clipboard and queues
// it as an attachment for the next turn. With no image in the clipboard (or on
// an unsupported platform) it surfaces a friendly notice and changes nothing.
func (m *tuiModel) pasteClipboardImage() (tea.Model, tea.Cmd) {
	data, mime, err := captureClipboardImage()
	if err != nil {
		m.println(noticeStyle.Render("📋 " + err.Error()))
		return m, nil
	}
	label := fmt.Sprintf("image (%s, %s)", shortMIME(mime), humanByteSize(len(data)))
	m.pendingAttachments = append(m.pendingAttachments, pendingAttachment{
		block: agent.NewImageBlock(mime, data),
		label: label,
	})
	return m, nil
}

// attachmentChips renders the pending attachments as a one-line summary for
// the echo / live view, e.g. "📎 image (PNG, 84 KB)".
func (m *tuiModel) attachmentChips() string {
	if len(m.pendingAttachments) == 0 {
		return ""
	}
	parts := make([]string, len(m.pendingAttachments))
	for i, a := range m.pendingAttachments {
		parts[i] = "📎 " + a.label
	}
	return strings.Join(parts, "  ")
}

// shortMIME turns "image/png" into "PNG" for the chip.
func shortMIME(mime string) string {
	if i := strings.LastIndex(mime, "/"); i >= 0 {
		return strings.ToUpper(mime[i+1:])
	}
	return strings.ToUpper(mime)
}

// humanTokens renders a token count compactly (e.g. 142000 → "142k").
func humanTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// lastRunes returns the final n runes of s (rune-safe so multi-byte CJK
// summaries aren't sliced mid-character). Used to bound the live compaction
// preview without growing it without limit.
func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// streamVerbFor labels the live tool-argument-streaming indicator with a
// gerund so it reads as an action in progress ("Writing… 12.3 KB"). Falls back
// to the card verb, then the raw tool name for tools without a friendly verb.
func streamVerbFor(toolName string) string {
	switch toolName {
	case "write_file":
		return "Writing"
	case "edit_file":
		return "Editing"
	case "terminal":
		return "Preparing command"
	}
	if v := cardVerbFor(toolName); v != "" {
		return v
	}
	return toolName
}

// humanByteSize renders a byte count compactly (B / KB / MB).
func humanByteSize(n int) string {
	const kb, mb = 1024, 1024 * 1024
	switch {
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/mb)
	case n >= kb:
		return fmt.Sprintf("%.1f KB", float64(n)/kb)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// submit acts on Enter. Idle → start a turn or dispatch a slash command.
// Running → steer, queue a slash command, or queue plain text. Empty input
// with no attachments is ignored.
func (m *tuiModel) submit() (tea.Model, tea.Cmd) {
	// If folded, expand to get the full text for submission.
	raw := m.ta.Value()
	if m.inputFolded {
		raw = m.foldedFullText
	}
	// text is what the model and history see (paste tokens restored to full
	// content); collapsed is what the scrollback echo shows, keeping any large
	// paste folded as "[#N pasted …]" rather than dumping it verbatim.
	text := strings.TrimSpace(m.expandPastes(raw))
	collapsed := strings.TrimSpace(raw)
	// A pending /goal edit consumes this submit first: empty text cancels,
	// a slash command cancels and dispatches normally (the user changed
	// their mind), anything else is the edited objective. Checked before
	// the empty early-return so clearing the prefill + Enter cancels.
	if m.goalEditPending {
		if !strings.HasPrefix(text, "/") {
			m.ta.Reset()
			m.inputHistoryIdx = -1
			m.clearPastes()
			return m.submitGoalEdit(text)
		}
		m.goalEditPending = false
		m.println(noticeStyle.Render("Goal edit cancelled"))
	}
	if text == "" && len(m.pendingAttachments) == 0 {
		return m, nil
	}
	// Note: a user message does NOT cancel the loop — the loop coexists with
	// the conversation (CC-style). Stopping is explicit: Ctrl+C, or the model
	// calling schedule_wakeup(cancel=true) when the user asks to stop.
	m.ta.Reset()
	m.inputHistoryIdx = -1
	m.clearPastes()
	// Clear folded state after submit
	if m.inputFolded {
		m.inputFolded = false
		m.foldedFullText = ""
		m.inputFoldedLines = 0
	}
	// Save to history for ↑/↓ recall (dedup consecutive identical lines).
	// Skip empty text (an image-only submit has nothing to recall).
	if text != "" && (len(m.inputHistory) == 0 || m.inputHistory[len(m.inputHistory)-1] != text) {
		m.inputHistory = append(m.inputHistory, text)
	}

	// Slash commands are the TUI's alone — the plain REPL is a pure conversation
	// loop. A leading "/" with attachments is treated as ordinary text, not a command.
	if strings.HasPrefix(text, "/") && len(m.pendingAttachments) == 0 {
		if !m.turnRunning {
			return m.dispatchSlash(text)
		}
		// Mid-turn slash commands are queued so they run after the current turn
		// finishes, instead of being misinterpreted as steer text.
		m.queue = append(m.queue, pendingItem{text: text})
		m.println(queueStyle.Render("＋ queued: " + text))
		return m, nil
	}

	if !m.turnRunning {
		// Fold any pending image attachments into this turn's user message.
		echo := collapsed
		if len(m.pendingAttachments) > 0 {
			blocks := make([]agent.ContentBlock, 0, len(m.pendingAttachments))
			for _, a := range m.pendingAttachments {
				blocks = append(blocks, a.block)
			}
			m.a.AttachUserBlocks(blocks)
			echo = strings.TrimSpace(collapsed + "  " + m.attachmentChips())
			m.pendingAttachments = nil
		}
		return m, m.startTurnEcho(text, echo)
	}

	// Mid-turn: enqueue the steer text, folding in any pending image
	// attachments so they ride this message rather than being stranded.
	if text != "" || len(m.pendingAttachments) > 0 {
		m.pendingSteer = append(m.pendingSteer, text)
		var blocks []agent.ContentBlock
		if len(m.pendingAttachments) > 0 {
			blocks = make([]agent.ContentBlock, 0, len(m.pendingAttachments))
			for _, a := range m.pendingAttachments {
				blocks = append(blocks, a.block)
			}
			m.pendingAttachments = nil
		}
		m.a.Inbox.EnqueueWithBlocks(text, blocks)
	}
	return m, nil
}

// dispatchSlash handles a leading-"/" line when the session is idle.
// Recognised commands are dispatched immediately; anything else falls through
// to startTurn as ordinary user text (paths, regexes, etc.) matching the plain
// REPL behaviour.
func (m *tuiModel) dispatchSlash(text string) (tea.Model, tea.Cmd) {
	cfg := m.cfg

	// /init: generate .octorules as a normal tool-enabled turn.
	if text == "/init" {
		if len(cfg.tools) == 0 || cfg.executor == nil {
			m.println(noticeStyle.Render("/init needs tools — restart with: octo --tools"))
			return m, nil
		}
		return m, m.startTurnEcho(initInstruction, "/init")
	}

	// /<skill> [args]: inline the skill body and run it as a turn.
	if s, args, ok := skillTrigger(cfg.skillReg, text); ok {
		echo := "/" + s.Name
		if args != "" {
			echo += " " + args
		}
		return m, m.startTurnEcho(inlineSkill(s, args), echo)
	}

	first := strings.Fields(text)[0]
	cmd := strings.ToLower(first)
	switch cmd {
	case "/exit", "/quit":
		m.quit = true
		return m, tea.Quit
	case "/model":
		return m.dispatchModel(strings.TrimSpace(strings.TrimPrefix(text, first)))
	case "/thinking":
		return m.dispatchThinking(strings.TrimSpace(strings.TrimPrefix(text, first)))
	case "/clear":
		return m.dispatchClear()
	case "/goal":
		return m.dispatchGoal(strings.TrimSpace(strings.TrimPrefix(text, first)))
	case "/compact":
		if m.turnRunning {
			m.println(noticeStyle.Render("/compact: wait for the current turn to finish"))
			return m, nil
		}
		return m, m.startCompact()
	case "/help", "/save", "/sessions", "/skills", "/memory", "/mcp", "/workflows":
		var b bytes.Buffer
		switch cmd {
		case "/help":
			printTuiHelp(&b)
		case "/save":
			if err := saveSession(&b, cfg.session, m.a); err != nil {
				fmt.Fprintf(&b, "save: %v\n", err)
			}
		case "/sessions":
			if err := printSessions(&b); err != nil {
				fmt.Fprintf(&b, "sessions: %v\n", err)
			}
		case "/skills":
			printSkills(&b, cfg.skillReg)
		case "/memory":
			printMemory(&b, cfg.memDir)
		case "/mcp":
			printMCP(&b)
		case "/workflows":
			printWorkflows(&b)
		}
		m.println(strings.TrimRight(b.String(), "\n"))
		return m, nil
	default:
		// Not a recognised command — treat it as ordinary user text so
		// paths, regexes, and other /-prefixed messages reach the model.
		return m, m.startTurn(text)
	}
}

// dispatchClear handles "/clear" — wipe the conversation history for a fresh
// start, keeping the system prompt, model, and tools. The cleared history is
// persisted immediately so a resume doesn't bring it back. Already-printed
// scrollback stays on screen (the terminal owns it); only the model's context
// is reset.
func (m *tuiModel) dispatchClear() (tea.Model, tea.Cmd) {
	if m.turnRunning {
		m.println(noticeStyle.Render("/clear: wait for the current turn to finish"))
		return m, nil
	}
	m.a.ClearHistory()
	if !m.cfg.noSave {
		m.cfg.session.SyncFrom(m.a.History)
		_ = m.cfg.session.Save()
	}
	m.assistantFirstBlock = true
	m.println(noticeStyle.Render("✦ context cleared — starting fresh"))
	return m, nil
}

// dispatchModel handles "/model <name>" — switch the active model for the
// current session. The provider stays the same; only the model identifier
// changes, so the sender does not need rebuilding.
func (m *tuiModel) dispatchModel(name string) (tea.Model, tea.Cmd) {
	if name == "" {
		m.println(errorStyle.Render("Usage: /model <model-name>"))
		return m, nil
	}
	m.a.Model = name
	m.cfg.modelName = name
	// Tool surface may differ per model (e.g. vision vs non-vision).
	if m.cfg.tools != nil {
		m.cfg.tools = tools.DefaultToolsFor(name)
	}
	m.println(noticeStyle.Render(fmt.Sprintf("Model: %s", name)))
	return m, nil
}

// dispatchThinking handles "/thinking <off|low|medium|high|xhigh|max>" — change the
// reasoning effort level. This rebuilds the sender because thinkingBudget and
// reasoningEffort are set at construction time.
func (m *tuiModel) dispatchThinking(level string) (tea.Model, tea.Cmd) {
	level = strings.ToLower(level)
	if level == "" {
		level = "off"
	}
	if level != "off" && !validReasoningEffort(level) {
		m.println(errorStyle.Render("Usage: /thinking off | low | medium | high | xhigh | max"))
		return m, nil
	}

	if m.cfg.providerName == "" {
		m.println(errorStyle.Render("Provider not set — cannot rebuild sender"))
		return m, nil
	}

	tuning := senderTuning{showReasoning: m.cfg.showReasoning}
	if level != "off" {
		tuning.reasoningEffort = level
		tuning.thinkingBudget = anthropicThinkingBudget(level)
	}

	newSender, err := buildSender(m.cfg.providerName, m.cfg.configEntry, m.cfg.stderr, tuning)
	if err != nil {
		m.println(errorStyle.Render(fmt.Sprintf("Failed to rebuild sender: %v", err)))
		return m, nil
	}

	m.a.Sender = newSender
	m.cfg.reasoningEffort = level
	if level == "off" {
		level = "off"
	}
	m.println(noticeStyle.Render(fmt.Sprintf("Thinking: %s", level)))
	return m, nil
}

func (m *tuiModel) interrupt() {
	if m.cancelTurn != nil {
		m.cancelTurn()
	}
	m.cancelWakeup()
	m.pendingSteer = nil
}

// ── modal (Ask) ──

func (m *tuiModel) openModal(msg askMsg) {
	st := &modalState{prompt: msg.prompt, resp: msg.resp, selected: map[int]bool{}}
	if msg.prompt.Kind == KindQuestion {
		st.options = append(st.options, msg.prompt.Options...)
		st.options = append(st.options, "Other (free text)")
	}
	st.cursor = 0
	st.otherActive = false
	st.otherInput = textinput.New()
	st.otherInput.Prompt = ""
	m.modal = st
}

// answerModal sends a response and clears the modal.
func (m *tuiModel) answerModal(r UserResponse) {
	if m.modal == nil {
		return
	}
	select {
	case m.modal.resp <- r:
	default:
	}
	m.modal = nil
}

func (m *tuiModel) handleModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := m.modal
	if st.prompt.Kind == KindPermission {
		switch {
		case keyIs(msg, 'y'):
			m.answerModal(UserResponse{Allow: true})
		case keyIs(msg, 'a'):
			m.answerModal(UserResponse{Allow: true, Always: true})
		case msg.Type == tea.KeyEsc, keyIs(msg, 'n'):
			m.answerModal(UserResponse{Allow: false})
		}
		return m, nil
	}

	// Inline free-text input for the "Other" option, backed by a real
	// textinput.Model so it gets cursor movement, word-jump, and
	// delete-forward for free — the same editing quality as the main input
	// box, instead of a hand-rolled append/backspace-only buffer (#1097).
	// Esc/Enter are intercepted before reaching the widget: Esc always
	// cancels the modal (textinput has no cancel key of its own), and Enter
	// confirms rather than inserting (textinput is single-line and ignores
	// Enter anyway, but intercepting keeps the intent explicit).
	if st.otherActive {
		switch msg.Type {
		case tea.KeyEsc:
			m.answerModal(UserResponse{Cancelled: true})
			return m, nil
		case tea.KeyEnter:
			if trimmed := strings.TrimSpace(st.otherInput.Value()); trimmed != "" {
				m.answerModal(UserResponse{Custom: trimmed})
			} else {
				m.answerModal(UserResponse{Cancelled: true})
			}
			return m, nil
		}
		var cmd tea.Cmd
		st.otherInput, cmd = st.otherInput.Update(msg)
		return m, cmd
	}

	// Question modal: arrow/j-k to move, space to toggle (multi), enter to
	// confirm, esc to cancel.
	switch msg.Type {
	case tea.KeyEsc:
		m.answerModal(UserResponse{Cancelled: true})
	case tea.KeyUp:
		if st.cursor > 0 {
			st.cursor--
		}
	case tea.KeyDown:
		if st.cursor < len(st.options)-1 {
			st.cursor++
		}
	case tea.KeySpace:
		if st.prompt.MultiSelect {
			st.selected[st.cursor] = !st.selected[st.cursor]
		}
	case tea.KeyEnter:
		m.confirmQuestion(st)
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "j":
			if st.cursor < len(st.options)-1 {
				st.cursor++
			}
		case "k":
			if st.cursor > 0 {
				st.cursor--
			}
		}
	}
	return m, nil
}

// confirmQuestion maps the modal selection onto a UserResponse. Selecting the
// trailing "Other" slot switches the modal into inline free-text input rather
// than cancelling.
func (m *tuiModel) confirmQuestion(st *modalState) {
	otherIdx := len(st.options) - 1

	if st.prompt.MultiSelect {
		wantOther := st.selected[otherIdx]
		if wantOther {
			st.otherActive = true
			st.otherInput.Focus()
			return
		}
		var picks []string
		for i := range st.options {
			if i == otherIdx {
				continue
			}
			if st.selected[i] {
				picks = append(picks, st.prompt.Options[i])
			}
		}
		if len(picks) == 0 {
			m.answerModal(UserResponse{Cancelled: true})
			return
		}
		m.answerModal(UserResponse{Choices: picks})
		return
	}

	if st.cursor == otherIdx {
		st.otherActive = true
		st.otherInput.Focus()
		return
	}
	m.answerModal(UserResponse{Choices: []string{st.prompt.Options[st.cursor]}})
}

// keyIs reports whether msg is a single-rune press of r (case-insensitive).
func keyIs(msg tea.KeyMsg, r rune) bool {
	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return false
	}
	got := msg.Runes[0]
	return got == r || got == r-32 || got == r+32
}

// renderWorkflowsPanel returns the workflow bottom-panel if any workflows are
// running, otherwise an empty string.
func (m *tuiModel) renderWorkflowsPanel() string {
	if len(m.workflows) == 0 {
		return ""
	}
	frame := m.spinnerGlyph()
	var lines strings.Builder
	for i, id := range m.workflowOrder() {
		wf := m.workflows[id]
		if wf == nil {
			continue
		}
		if i > 0 {
			lines.WriteByte('\n')
		}
		label := truncate1LineOr(wf.description, id)
		elapsed := time.Since(wf.start).Round(time.Second)
		status := wf.status
		if status == "" {
			status = "running"
		}
		chain := truncate1LineOr(wf.lastLine, "starting…")
		fmt.Fprintf(&lines, "  %c %s — %s  (%s, %s)",
			frame, label, chain, status, elapsed)
	}
	return tui.Panel(fmt.Sprintf("workflows (%d running)", len(m.workflows)), lines.String()) + "\n"
}

// ── View ──

func (m *tuiModel) View() string {
	if m.quit {
		return ""
	}

	var b strings.Builder

	// Deferred user-message echo: shown live above the activity area until the
	// turn's first output commits it to the scrollback (or Esc takes it back).
	if m.echoPending != "" {
		b.WriteString(m.echoPending)
		b.WriteByte('\n')
	}

	// Live partial assistant text
	if p := m.partial.String(); p != "" {
		rendered := m.md.render(p, m.width)
		if m.assistantFirstBlock {
			rendered = injectAssistantPrefix(rendered, assistantPrefixStyle.Render("◆ "))
		}
		b.WriteString(rendered)
		b.WriteByte('\n')
	}

	// Activity indicator
	if m.compacting {
		// Compaction runs between LLM calls; surface a dedicated spinner with a
		// live "generated ~N / max tokens" readout plus a streaming preview so
		// the user can tell the conversation is being summarized, not frozen.
		label := "Compacting conversation history…"
		if m.compactMaxTokens > 0 {
			label += fmt.Sprintf("  ~%s / ~%s tokens",
				humanTokens(m.compactTokens), humanTokens(m.compactMaxTokens))
		}
		b.WriteString(m.spinnerLine(label, m.compactStart))
		b.WriteByte('\n')
		if preview := strings.TrimSpace(strings.ReplaceAll(m.compactPreview, "\n", " ")); preview != "" {
			b.WriteString(hintStyle.Render("  … " + lastRunes(preview, 100)))
			b.WriteByte('\n')
		}
	} else if m.running != nil {
		b.WriteString(m.spinnerLine(m.running.verb+"("+m.running.target+")", m.running.start))
		b.WriteByte('\n')
		if tools.HasActiveSync() || tools.HasActiveSubAgentSync() {
			b.WriteString(hintStyle.Render("  [Ctrl+B] background  [Esc] interrupt"))
			b.WriteByte('\n')
		}
	} else if m.turnRunning && m.toolStreamName != "" {
		// The model is streaming a tool call's arguments (e.g. a large
		// write_file body). Show a live byte counter so a multi-second argument
		// stream reads as progress, not a freeze. The count is JSON-stream bytes
		// (a touch larger than the final value), which is fine for a heartbeat.
		label := fmt.Sprintf("%s… %s", streamVerbFor(m.toolStreamName), humanByteSize(m.toolStreamBytes))
		b.WriteString(m.spinnerLine(label, m.turnStart))
		b.WriteByte('\n')
	} else if m.turnRunning && m.partial.Len() == 0 {
		// Turn is running but nothing is on the activity line right now — no
		// live tool and no streaming text. That's the wait on the model
		// (initial prompt, or between steps after a tool result). Show the
		// thinking spinner so the user can tell the turn isn't idle. While
		// text is actively streaming (partial non-empty), the text itself is
		// the feedback, so the spinner stays out of the way.
		b.WriteString(m.thinkingLine())
		b.WriteByte('\n')
	}

	// Live task list — attached under the activity area while a turn runs
	// (Claude Code style), or pinned via Ctrl+T. Self-suppressing when there's
	// no outstanding work, unless pinned (see taskListView).
	if m.turnRunning || m.showTasks {
		if tl := m.taskListView(); tl != "" {
			b.WriteString(tl)
			b.WriteByte('\n')
		}
	}

	// Queue panel
	if len(m.queue) > 0 {
		var items strings.Builder
		for i, q := range m.queue {
			if i > 0 {
				items.WriteByte('\n')
			}
			// A queued item is user-typed and can be arbitrarily long or
			// span multiple lines; render it as one bounded line so it
			// can't blow apart the panel border (#1095).
			items.WriteString(queueStyle.Render(fmt.Sprintf("%d. %s", i+1, truncate1Line(q.text))))
		}
		b.WriteString(tui.Panel(fmt.Sprintf("queue (%d)", len(m.queue)), items.String()))
		b.WriteByte('\n')
	}

	// Background shells — a single dim line while idle (Claude Code style:
	// "✳ 26s · 1 shell still running"). While a turn runs the status bar's
	// shell count is enough: the launch is already in the transcript and the
	// exit notice will land there too, so a per-command panel just repeats
	// what's known.
	if bg := tools.RunningBackground(); len(bg) > 0 && !m.turnRunning {
		frame := m.spinnerGlyph()
		b.WriteString(hintStyle.Render(fmt.Sprintf("%c %s · %s",
			frame, time.Since(bg[0].Start).Round(time.Second), shellCountLabel(len(bg)))))
		b.WriteByte('\n')
	}

	// Sub-agents panel — live tool-call chain of each running sub-agent
	// (Claude-Code style), mirroring the background-processes panel.
	if len(m.subAgentOrder) > 0 {
		frame := m.spinnerGlyph()
		var lines strings.Builder
		for i, id := range m.subAgentOrder {
			sa := m.subAgents[id]
			if sa == nil {
				continue
			}
			if i > 0 {
				lines.WriteByte('\n')
			}
			// Show the task description as the name, prefixed with the
			// subagent_type (e.g. "explore (Find code)"). An untyped child is a
			// fork of the parent, labelled "fork" — the tool's own vocabulary.
			// The agent_N id is the internal addressing handle, not user-facing.
			label := id
			if sa.description != "" {
				label = truncate1Line(sa.description)
			}
			typ := sa.agentType
			if typ == "" {
				typ = "fork"
			}
			label = fmt.Sprintf("%s (%s)", typ, label)
			focus := "  "
			if m.subAgentFocus == i {
				focus = "▸ "
			}
			chain := "starting…"
			if len(sa.recent) > 0 {
				chain = strings.Join(sa.recent, " · ")
			}
			elapsed := time.Since(sa.start).Round(time.Second)
			fmt.Fprintf(&lines, "%s%c %s — %s  (%d tools, %s)",
				focus, frame, label, chain, sa.toolCount, elapsed)
			if sa.expanded && len(sa.history) > 0 {
				for _, h := range sa.history {
					lines.WriteByte('\n')
					lines.WriteString("    ▸ " + h)
				}
			}
		}
		title := fmt.Sprintf("sub-agents (%d running)", len(m.subAgentOrder))
		if m.subAgentFocus >= 0 {
			title += "  [↑/↓ nav · Enter expand · Esc back]"
		}
		b.WriteString(tui.Panel(title, lines.String()))
		b.WriteByte('\n')
	}

	b.WriteString(m.renderWorkflowsPanel())

	// Pending steer — show what the user typed mid-turn, indented right above
	// the input box (Claude Code style: input上方，比普通消息多了一个indent).
	// Once the agent loop drains the inbox these messages are printed to the
	// scrollback in handleTurnFinished so they appear in the transcript like
	// regular user messages.
	if len(m.pendingSteer) > 0 {
		for _, s := range m.pendingSteer {
			b.WriteString(pendingSteerStyle.Render("  > ") + pendingSteerStyle.Render(s))
			b.WriteByte('\n')
		}
	}

	// Pending image attachments — chips above the input so the user knows an
	// image will ride their next message (Esc discards them).
	if len(m.pendingAttachments) > 0 {
		b.WriteString(pendingSteerStyle.Render("  " + m.attachmentChips()))
		b.WriteByte('\n')
	}

	// An active Ask prompt (permission / question) takes the input box's place
	// while everything above — streaming text, activity spinner, task list,
	// panels — stays visible, so the user decides with the model's reasoning
	// still on screen instead of a bare full-screen dialog.
	if m.modal != nil {
		b.WriteString(m.modalView())
		return b.String()
	}

	// Slash-command completion menu, right above the input box (Claude Code style).
	b.WriteString(m.completionView())

	// Input box + status bar
	b.WriteString(m.renderInputBox())
	b.WriteByte('\n')
	b.WriteString(m.renderStatusBar())
	return b.String()
}

// updateTextAreaHeight sets the textarea height to match the number of lines
// in the current value, capped at a maximum so it doesn't take over the screen.
// When the height grows we also reset the viewport YOffset to 0 via reflection
// so that earlier lines remain visible instead of being scrolled out of view.
func (m *tuiModel) updateTextAreaHeight() tea.Cmd {
	lines := m.softWrappedRows()
	maxH := min(6, m.height/4)
	if maxH < 1 {
		maxH = 1
	}
	newH := min(lines, maxH)
	if m.ta.Height() == newH {
		return nil
	}
	m.ta.SetHeight(newH)
	// textarea.viewport is unexported; use unsafe to reset YOffset.
	v := reflect.ValueOf(&m.ta).Elem().FieldByName("viewport")
	if !v.IsValid() || v.IsNil() {
		return nil
	}
	vp := reflect.NewAt(v.Elem().Type(), unsafe.Pointer(v.Elem().UnsafeAddr())).Elem()
	if yOffset := vp.FieldByName("YOffset"); yOffset.IsValid() {
		yOffset.SetInt(0)
	}
	return nil
}

// softWrappedRows returns the number of display rows the textarea value
// occupies after soft-wrapping at the current width. This matches bubbles'
// internal wrap logic so the input box grows with the actual rendered text
// instead of just counting hard newlines.
func (m *tuiModel) softWrappedRows() int {
	// Read bubbles' internal wrap width so our row count matches what the
	// textarea will actually render. Fall back to newline counting if the
	// internals ever change shape.
	v := reflect.ValueOf(&m.ta).Elem()
	// Guard the field lookup: if a bubbles upgrade renames/removes the
	// unexported "width" field, FieldByName returns the zero Value and .Int()
	// would panic, crashing the TUI on the next keystroke. Fall back to the
	// newline count instead (same fallback the width<=0 branch already uses).
	wf := v.FieldByName("width")
	if !wf.IsValid() || wf.Kind() != reflect.Int {
		return strings.Count(m.ta.Value(), "\n") + 1
	}
	width := int(wf.Int())
	if width <= 0 {
		return strings.Count(m.ta.Value(), "\n") + 1
	}
	total := 0
	for _, line := range strings.Split(m.ta.Value(), "\n") {
		total += len(wrapRunes([]rune(line), width))
	}
	return total
}

func (m *tuiModel) renderInputBox() string {
	if m.inputFolded {
		// When folded, show a compact placeholder instead of the full textarea.
		// The textarea still exists (holds cursor, etc.) but is hidden.
		label := fmt.Sprintf("[ %d lines pasted · Tab to expand ]", m.inputFoldedLines)
		return hintStyle.Render(label)
	}
	return m.ta.View()
}

// renderStatusBar renders the model / thinking / cwd / context% / permission
// segments, with a separator line above and the contextual key hint below
// (Claude Code style).
func (m *tuiModel) renderStatusBar() string {
	var segs [][2]string
	if m.cfg.modelName != "" {
		segs = append(segs, [2]string{"model", m.cfg.modelName})
	}
	if m.cfg.reasoningEffort != "" {
		segs = append(segs, [2]string{"think", m.cfg.reasoningEffort})
	}
	if m.cwd != "" {
		segs = append(segs, [2]string{"cwd", m.cwd})
	}
	if used, window := m.a.ContextUsage(); window > 0 && used > 0 {
		pct := used * 100 / window
		if pct > 100 {
			pct = 100
		}
		segs = append(segs, [2]string{"ctx", fmt.Sprintf("%d%%", pct)})
	}
	if m.cfg.permEngine != nil {
		segs = append(segs, [2]string{"perm", string(m.cfg.permEngine.GetMode())})
	}
	if m.goalsWired() {
		if g, ok := m.cfg.session.GoalSnapshot(); ok {
			segs = append(segs, [2]string{"goal", goalStatusSegment(g)})
		}
	}
	if n := len(tools.RunningBackground()); n > 0 {
		label := "1 shell"
		if n > 1 {
			label = fmt.Sprintf("%d shells", n)
		}
		segs = append(segs, [2]string{"shell", label})
	}

	// Key hints live in the startup banner, not here, and the running-turn
	// duration is intentionally omitted — the status bar stays a compact
	// model / cwd / context% / perm-mode / shell-count strip.
	return tui.StatusBar(segs, "", m.width)
}

// shellCountLabel renders the background-shell counter shown in the status
// bar and the idle activity line ("1 shell still running" / "3 shells …").
func shellCountLabel(n int) string {
	if n == 1 {
		return "1 shell still running"
	}
	return fmt.Sprintf("%d shells still running", n)
}

// workingDir returns the current directory, or "" if it can't be determined.
func workingDir() string {
	d, err := os.Getwd()
	if err != nil {
		return ""
	}
	return d
}

// abbreviateHome replaces the user's home-dir prefix with "~".
func abbreviateHome(path string) string {
	if path == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if path == home {
			return "~"
		}
		if strings.HasPrefix(path, home+string(os.PathSeparator)) {
			return "~" + path[len(home):]
		}
	}
	return path
}

// permissionDetailMaxLines caps the command / input preview inside the
// permission prompt. Most real commands fit; the fold marker makes a longer
// one visible as "there is more you haven't seen" rather than silently hiding it.
const permissionDetailMaxLines = 10

// permissionValueCap bounds one input value's preview in the generic
// key: value rendering. Generous — the point of the prompt is that the user
// sees what they're approving.
const permissionValueCap = 200

func (m *tuiModel) modalView() string {
	st := m.modal
	var b strings.Builder

	if st.prompt.Kind == KindPermission {
		// Unboxed on purpose: the body must show the full command / diff, and
		// long lines inside a lipgloss border make the box wider than the
		// terminal and garble it. Lines are hard-wrapped to the width here —
		// bubbletea's inline renderer truncates every frame line at terminal
		// width with NO marker, so anything past the width would silently
		// vanish from an approval prompt.
		//
		// The rendered detail is cached on the modal: View() runs on every
		// ~120ms spinner tick while the prompt is open, and the edit_file
		// path reads the target file each time it renders.
		if !st.detailSet || st.detailWidth != m.width {
			st.detail = renderPermissionDetail(st.prompt.ToolName, st.prompt.ToolInput, m.width)
			st.detailWidth = m.width
			st.detailSet = true
		}
		b.WriteString(modalStyle.Render("⚠ permission — " + st.prompt.ToolName))
		b.WriteByte('\n')
		b.WriteString(st.detail)
		b.WriteByte('\n')
		b.WriteString(hintStyle.Render("[y]es · [a]lways this session · [n]o/Esc"))
		return b.String()
	}

	header := st.prompt.Header
	if header == "" {
		header = "question"
	}
	b.WriteString(modalStyle.Render("[" + header + "]"))
	b.WriteByte('\n')
	b.WriteString("  " + st.prompt.Question + "\n")
	for i, opt := range st.options {
		cursor := "  "
		if i == st.cursor {
			cursor = "▸ "
		}
		mark := ""
		if st.prompt.MultiSelect {
			if st.selected[i] {
				mark = "[x] "
			} else {
				mark = "[ ] "
			}
		}
		b.WriteString(fmt.Sprintf("  %s%s%s\n", cursor, mark, opt))
	}
	if st.otherActive {
		b.WriteString("  Other: " + st.otherInput.View() + "\n")
		b.WriteString(hintStyle.Render("Enter confirm · Esc cancel"))
		return tui.Box(b.String())
	}
	hint := "↑/↓ move · Enter select · Esc cancel"
	if st.prompt.MultiSelect {
		hint = "↑/↓ move · Space toggle · Enter confirm · Esc cancel"
	}
	b.WriteString(hintStyle.Render(hint))
	return tui.Box(b.String())
}

// permissionMaxKeys caps how many input keys the generic listing shows.
const permissionMaxKeys = 10

// renderPermissionDetail renders what a permission prompt is actually asking
// to do: the full command for terminal, the diff for edit_file, and a
// key: value listing for everything else. Every path favours visibility over
// tidiness — the user is authorizing this content, so nothing load-bearing
// may be truncated to a one-line summary (the pre-#1092 behaviour capped the
// whole thing at 60 runes).
func renderPermissionDetail(toolName string, input map[string]any, width int) string {
	switch toolName {
	case "terminal":
		if cmd, _ := input["command"].(string); strings.TrimSpace(cmd) != "" {
			return renderPermissionBlock(cmd, width)
		}
	case "edit_file":
		path, _ := input["path"].(string)
		oldS, _ := input["old_string"].(string)
		newS, _ := input["new_string"].(string)
		if path != "" {
			// Tabs stay intact: the card expands them itself, and new_string
			// must match the file bytes for the card's line-number lookup
			// (the card normalizes CRLF on its side, matching the \r drop here).
			// The path is sanitized too — it renders in the card header, the
			// same display surface as the rest of the prompt.
			return tui.RenderEditCard(sanitizeControls(path, false),
				sanitizeControls(oldS, false), sanitizeControls(newS, false), width)
		}
	}
	return renderPermissionGeneric(input, width)
}

// plainPermissionDetail is renderPermissionDetail for the plain/stdin prompt:
// same visibility guarantees, but edit_file falls back to the generic listing
// (the ANSI diff card is TUI-only) and width 0 skips wrapping — the plain
// path writes to a real terminal, which wraps naturally.
func plainPermissionDetail(toolName string, input map[string]any) string {
	if cmd, _ := input["command"].(string); toolName == "terminal" && strings.TrimSpace(cmd) != "" {
		return renderPermissionBlock(cmd, 0)
	}
	return renderPermissionGeneric(input, 0)
}

// renderPermissionGeneric lists a tool's input as sorted "key: value" lines.
// Multi-line values (SQL, write_file content, MCP payloads) show their first
// few lines — enough to judge what's being approved — then fold, so one value
// can't swallow the whole prompt; the rune cap bounds a single long line.
func renderPermissionGeneric(input map[string]any, width int) string {
	if len(input) == 0 {
		return hintStyle.Render("  (no input)")
	}
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	shown := keys
	if len(shown) > permissionMaxKeys {
		shown = shown[:permissionMaxKeys]
	}
	for i, k := range shown {
		if i > 0 {
			b.WriteByte('\n')
		}
		v := sanitizeForPrompt(fmt.Sprintf("%v", input[k]))
		// Keys come from the model's tool-call JSON too — the same injection
		// surface as values.
		key := sanitizeForPrompt(k)
		lines := strings.Split(v, "\n")
		if len(lines) == 1 {
			b.WriteString(wrapIndented(key+": "+truncateRunes(v, permissionValueCap), "  ", width))
			continue
		}
		const perValueLines = 4
		vshown := lines
		if len(lines) > perValueLines {
			vshown = lines[:perValueLines]
		}
		b.WriteString(wrapIndented(key+":", "  ", width))
		for _, l := range vshown {
			b.WriteString("\n" + wrapIndented(truncateRunes(l, permissionValueCap), "    ", width))
		}
		if extra := len(lines) - len(vshown); extra > 0 {
			b.WriteString("\n    " + hintStyle.Render(fmt.Sprintf("… +%d more lines", extra)))
		}
	}
	if extra := len(keys) - len(shown); extra > 0 {
		b.WriteString("\n  " + hintStyle.Render(fmt.Sprintf("… +%d more", extra)))
	}
	return b.String()
}

// renderPermissionBlock renders a multi-line text body (a shell command)
// indented, capped at permissionDetailMaxLines with a fold marker. Lines are
// hard-wrapped to width rather than clipped: bubbletea's inline renderer
// truncates frame lines at terminal width with no marker, and a permission
// prompt hiding the tail of a long command would be approving blind. The line
// cap applies to logical lines only — a single long wrapped line stays fully
// visible.
func renderPermissionBlock(text string, width int) string {
	lines := strings.Split(strings.TrimRight(sanitizeForPrompt(text), "\n"), "\n")
	shown, extra := lines, 0
	if len(lines) > permissionDetailMaxLines {
		shown, extra = lines[:permissionDetailMaxLines], len(lines)-permissionDetailMaxLines
	}
	var b strings.Builder
	for i, l := range shown {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(wrapIndented(l, "  ", width))
	}
	if extra > 0 {
		b.WriteString("\n  " + hintStyle.Render(fmt.Sprintf("… +%d more lines", extra)))
	}
	return b.String()
}

// sanitizeForPrompt neutralizes control characters in model-supplied text
// before it is shown in an approval prompt, so what the user reads is what
// executes: a raw \r would reposition the cursor and let a command overwrite
// its own dangerous prefix on screen; a raw ESC could inject cursor-movement
// or erase sequences. Newlines survive, tabs expand (RuneWidth counts \t as 0,
// which would break the wrap math), \r is dropped, and every other C0/DEL
// byte renders in caret notation (ESC → ^[).
func sanitizeForPrompt(s string) string { return sanitizeControls(s, true) }

// sanitizeControls is sanitizeForPrompt's core; expandTab=false keeps tabs
// intact for consumers that expand them themselves and need byte-exact text
// (the edit_file diff card's line-number lookup).
func sanitizeControls(s string, expandTab bool) string {
	hazard := func(r rune) bool {
		return (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f)
	}
	if !strings.ContainsFunc(s, hazard) {
		if !expandTab || !strings.ContainsRune(s, '\t') {
			return s
		}
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteRune(r)
		case r == '\t':
			if expandTab {
				b.WriteString("    ")
			} else {
				b.WriteRune(r)
			}
		case r == '\r':
			// Dropped: mid-line it would home the cursor; as part of \r\n it
			// is redundant with the surviving \n.
		case r < 0x20:
			b.WriteByte('^')
			b.WriteByte(byte(r) ^ 0x40)
		case r == 0x7f:
			b.WriteString("^?")
		case r >= 0x80 && r <= 0x9f:
			// Decoded C1 controls (e.g. U+009B = CSI): inert on most modern
			// emulators but xterm-lineage ones interpret them. Replace with a
			// visible placeholder.
			b.WriteRune('�')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// wrapIndented prefixes line with indent and hard-wraps it to width display
// cells, indenting continuation rows the same amount. width <= 0 (unknown)
// emits a single indented line. Wrapping is by display width (CJK = 2 cells),
// character-exact — an approval prompt must show every character, and
// bubbletea would otherwise truncate the overflow invisibly.
func wrapIndented(line, indent string, width int) string {
	avail := width - rw.StringWidth(indent)
	if width <= 0 || avail < 1 || rw.StringWidth(line) <= avail {
		return indent + line
	}
	var b strings.Builder
	var cur strings.Builder
	w := 0
	flush := func() {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(indent + cur.String())
		cur.Reset()
		w = 0
	}
	for _, r := range line {
		rw_ := rw.RuneWidth(r)
		if w+rw_ > avail && cur.Len() > 0 {
			flush()
		}
		cur.WriteRune(r)
		w += rw_
	}
	if cur.Len() > 0 {
		flush()
	}
	return b.String()
}

// cacheLine formats the per-turn cache footer, or "" when nothing to show.
// Verbose-only: at default verbosity the footer would land after every turn
// (cache moves on essentially every Anthropic-protocol call) and the status
// bar's ctx% already covers the at-a-glance need.
func cacheLine(v verbosity, reply agent.Reply) string {
	if !v.verbose() {
		return ""
	}
	return noticeStyle.Render(fmt.Sprintf("  ⓘ cache: %d read, %d write (in %d / out %d)",
		reply.CacheReadTokens, reply.CacheWriteTokens, reply.InputTokens, reply.OutputTokens))
}

// thinkingPhrases rotate (slowly) on the initial-wait placeholder so the
// pause feels alive, CC-style. Cycled by spinnerFrame.
var thinkingPhrases = []string{"Thinking", "Pondering", "Working", "Reasoning"}

func (m *tuiModel) thinkingPhrase() string {
	return thinkingPhrases[(m.spinnerFrame/16)%len(thinkingPhrases)]
}

// tuiSpinnerFrames is the in-app activity spinner: a center-out "breathing"
// star that grows then shrinks (Claude Code style) instead of a scattered
// braille wheel. Adjacent glyphs differ only by one size step, so the motion
// reads as a calm pulse rather than a flicker. (The headless one-shot spinner
// keeps its own braille frames — see spinner.go.)
var tuiSpinnerFrames = []rune{'·', '✢', '✳', '∗', '✻', '✽', '✻', '∗', '✳', '✢'}

// spinnerGlyph returns the current activity-spinner frame. The visible frame
// advances at half the tick rate (~240ms/frame) so the pulse stays unhurried
// while the elapsed clock above it still updates every second.
func (m *tuiModel) spinnerGlyph() rune {
	return tuiSpinnerFrames[(m.spinnerFrame/2)%len(tuiSpinnerFrames)]
}

// spinnerLine renders one animated activity line: a spinner frame, a label,
// and elapsed seconds since the given start.
func (m *tuiModel) spinnerLine(label string, since time.Time) string {
	frame := m.spinnerGlyph()
	return hintStyle.Render(fmt.Sprintf("%c %s (%s)", frame, label, time.Since(since).Round(time.Second)))
}

// thinkingLine is the wait-on-the-model activity line. Since the reasoning
// trace itself is not shown, the line carries the turn's elapsed time and a
// rough output-token count ("↓ ~N tokens", chars/4) so a long silent stretch
// still reads as the model working, Claude Code style. When a task is in
// progress its present-continuous form replaces the generic thinking verb
// ("Migrating config readers…" instead of "Thinking…").
func (m *tuiModel) thinkingLine() string {
	frame := m.spinnerGlyph()
	phrase := m.activeTaskPhrase()
	if phrase == "" {
		phrase = m.thinkingPhrase()
	}
	meta := time.Since(m.turnStart).Round(time.Second).String()
	if m.turnOutChars == 0 {
		// Uplink: the request/context is going up and nothing has streamed
		// back yet. Show how much context is being sent (last known
		// occupancy); 0 on the very first turn falls back to a bare arrow.
		if used, _ := m.a.ContextUsage(); used > 0 {
			meta += fmt.Sprintf(" · ↑ ~%s tokens", humanTokens(used))
		} else {
			meta += " · ↑"
		}
	} else {
		// Downlink: tokens are streaming back. At the hand-off to the answer
		// the counter sprints (see answerSprint) as an accelerating flourish
		// just before the prose appears.
		meta += fmt.Sprintf(" · ↓ ~%s tokens", humanTokens(m.sprintTokens()))
	}
	return fmt.Sprintf("%s %s %s",
		hintStyle.Render(string(frame)),
		activityStyle.Render(phrase+"…"),
		hintStyle.Render("("+meta+")"))
}

// sprintTokens is the token estimate shown on the downlink line. Normally it's
// the live estimate (chars/4); during the answer hand-off sprint it eases from
// the pre-sprint value up to the live estimate on an accelerating (ease-in)
// curve, so the counter visibly races just before the answer drops in.
func (m *tuiModel) sprintTokens() int {
	live := m.turnOutChars / 4
	if !m.answerSprint {
		return live
	}
	frac := float64(time.Since(m.sprintStart)) / float64(answerSprintDur)
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	eased := frac * frac // ease-in: slow start, accelerating finish
	return m.sprintStartTok + int(float64(live-m.sprintStartTok)*eased)
}

// injectAssistantPrefix strips glamour's 2-space paragraph indent from the
// first line and prepends prefix so ◆ aligns at column 0, mirroring "> ".
// Leading newlines are already trimmed by markdownRenderer.render.
//
// glamour emits the document margin as literal spaces sitting *behind*
// zero-width SGR escapes (e.g. "\x1b[…m\x1b[0m  \x1b[…m我先…"), so the string
// rarely begins with a space — a plain TrimLeft(rendered, " ") would be a
// no-op and the indent would survive. We instead walk the leading run,
// dropping margin spaces while keeping the escapes (the colour escape that
// styles the text included), and stop at the first visible glyph.
func injectAssistantPrefix(rendered, prefix string) string {
	var kept strings.Builder
	i := 0
	for i < len(rendered) {
		if rendered[i] == ' ' {
			i++ // drop glamour's margin spaces
			continue
		}
		if n := ansiPrefixLen(rendered[i:]); n > 0 {
			kept.WriteString(rendered[i : i+n]) // keep zero-width escapes
			i += n
			continue
		}
		break // first visible glyph
	}
	if i >= len(rendered) {
		return rendered // nothing but whitespace/escapes
	}
	return prefix + kept.String() + rendered[i:]
}

// ansiPrefixLen returns the byte length of the ANSI CSI escape sequence at the
// start of s (ESC '[' … final-byte in 0x40–0x7E, which covers the SGR colour
// codes glamour emits), or 0 if s does not begin with a complete one.
func ansiPrefixLen(s string) int {
	if len(s) < 2 || s[0] != 0x1b || s[1] != '[' {
		return 0
	}
	for i := 2; i < len(s); i++ {
		if s[i] >= 0x40 && s[i] <= 0x7e {
			return i + 1
		}
	}
	return 0 // unterminated; treat as not a complete escape
}

// wrapRunes is a local copy of bubbles/textarea.wrap. It soft-wraps a single
// line of runes to the given width, matching the textarea's rendering so the
// input box height grows with the actual displayed text. The implementation is
// reproduced here because bubbles does not export the function.
func wrapRunes(runes []rune, width int) [][]rune {
	var (
		lines  = [][]rune{{}}
		word   = []rune{}
		row    int
		spaces int
	)

	// Word wrap the runes
	for _, r := range runes {
		if unicode.IsSpace(r) {
			spaces++
		} else {
			word = append(word, r)
		}

		if spaces > 0 {
			if uniseg.StringWidth(string(lines[row]))+uniseg.StringWidth(string(word))+spaces > width {
				row++
				lines = append(lines, []rune{})
				lines[row] = append(lines[row], word...)
				lines[row] = append(lines[row], repeatSpacesRune(spaces)...)
				spaces = 0
				word = nil
			} else {
				lines[row] = append(lines[row], word...)
				lines[row] = append(lines[row], repeatSpacesRune(spaces)...)
				spaces = 0
				word = nil
			}
		} else {
			// If the last character is a double-width rune, then we may not be able to add it to this line
			// as it might cause us to go past the width.
			lastCharLen := rw.RuneWidth(word[len(word)-1])
			if uniseg.StringWidth(string(word))+lastCharLen > width {
				// If the current line has any content, let's move to the next
				// line because the current word fills up the entire line.
				if len(lines[row]) > 0 {
					row++
					lines = append(lines, []rune{})
				}
				lines[row] = append(lines[row], word...)
				word = nil
			}
		}
	}

	if uniseg.StringWidth(string(lines[row]))+uniseg.StringWidth(string(word))+spaces >= width {
		lines = append(lines, []rune{})
		lines[row+1] = append(lines[row+1], word...)
		// We add an extra space at the end of the line to account for the
		// trailing space at the end of the previous soft-wrapped lines so that
		// behaviour when navigating is consistent and so that we don't need to
		// continually add edges to handle the last line of the wrapped input.
		spaces++
		lines[row+1] = append(lines[row+1], repeatSpacesRune(spaces)...)
	} else {
		lines[row] = append(lines[row], word...)
		spaces++
		lines[row] = append(lines[row], repeatSpacesRune(spaces)...)
	}

	return lines
}

func repeatSpacesRune(n int) []rune {
	return []rune(strings.Repeat(string(' '), n))
}
