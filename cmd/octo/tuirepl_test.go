package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/permission"
	"github.com/open-octo/octo-agent/internal/tui"
)

// fakeProg records messages the sink sends, standing in for *tea.Program.
type fakeProg struct {
	mu   sync.Mutex
	msgs []tea.Msg
}

func (f *fakeProg) Send(m tea.Msg) {
	f.mu.Lock()
	f.msgs = append(f.msgs, m)
	f.mu.Unlock()
}

var (
	testHistoryDirOnce sync.Once
	testHistoryDir     string
	testHistorySeq     atomic.Int64
)

// isolateTestInputHistory points OCTO_INPUT_HISTORY_FILE at a fresh,
// nonexistent path per call, so newTestModel never reads or writes the
// developer's real ~/.octo/input_history, and successive tests in this
// package don't leak history entries into each other.
func isolateTestInputHistory() {
	testHistoryDirOnce.Do(func() {
		if d, err := os.MkdirTemp("", "octo-tui-test-history"); err == nil {
			testHistoryDir = d
		}
	})
	if testHistoryDir == "" {
		return
	}
	os.Setenv("OCTO_INPUT_HISTORY_FILE", filepath.Join(testHistoryDir, fmt.Sprintf("h%d", testHistorySeq.Add(1))))
}

func newTestModel() *tuiModel {
	isolateTestInputHistory()
	a := agent.New(&stubSender{reply: "ok"}, "m")
	m := newTUIModel(replConfig{a: a, noSave: true})
	m.sink = &tuiSink{prog: &fakeProg{}}
	return m
}

func setInput(m *tuiModel, s string) {
	m.ta.SetValue(s)
	m.ta.CursorEnd()
}

func TestTUI_IdleEnterStartsTurn(t *testing.T) {
	m := newTestModel()
	setInput(m, "hello")
	_, _ = m.submit()
	if !m.turnRunning {
		t.Fatal("idle Enter should start a turn")
	}
	if m.ta.Value() != "" {
		t.Errorf("input should clear after submit, got %q", m.ta.Value())
	}
}

func TestTUI_RunningEnterEnqueues(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	setInput(m, "also handle errors")
	_, _ = m.submit() // Enter
	if !m.a.Inbox.HasPending() {
		t.Fatal("Enter while running should enqueue to inbox")
	}
	items := m.a.Inbox.Drain()
	if len(items) != 1 || items[0].Text != "also handle errors" {
		t.Errorf("inbox = %v", items)
	}
	if len(m.queue) != 0 {
		t.Errorf("inbox must not enqueue to queue, queue = %+v", m.queue)
	}
}

func TestTUI_CtrlXUnqueuesMostRecent(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	m.queue = []pendingItem{{text: "first"}, {text: "second"}}

	// Ctrl+X drops the most-recently queued item.
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlX})
	if len(m.queue) != 1 || m.queue[0].text != "first" {
		t.Fatalf("Ctrl+X should drop the last queued item, got %+v", m.queue)
	}
	// Repeat clears the rest.
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlX})
	if len(m.queue) != 0 {
		t.Fatalf("repeated Ctrl+X should empty the queue, got %+v", m.queue)
	}
	// Ctrl+X on an empty queue is a harmless no-op.
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlX})
	if len(m.queue) != 0 {
		t.Errorf("Ctrl+X on empty queue should be a no-op")
	}
}

// Idle, the queue key behaves exactly like Enter (design §7): nothing would
// ever drain a queue with no running turn, so the message must start a turn
// immediately instead of being trapped.
func TestTUI_IdleCtrlQStartsTurn(t *testing.T) {
	m := newTestModel()
	setInput(m, "check recent PRs")
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	if !m.turnRunning {
		t.Fatal("idle Ctrl+Q should start a turn, same as Enter")
	}
	if len(m.queue) != 0 {
		t.Errorf("idle Ctrl+Q must not leave the message queued, got %+v", m.queue)
	}
	if m.ta.Value() != "" {
		t.Errorf("input should clear, got %q", m.ta.Value())
	}
}

func TestTUI_RunningCtrlQQueues(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	setInput(m, "run the linter")
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	if len(m.queue) != 1 || m.queue[0].text != "run the linter" {
		t.Fatalf("Ctrl+Q should queue, got %+v", m.queue)
	}
	if m.a.Inbox.HasPending() {
		t.Error("queue must not enqueue to inbox")
	}
}

// A slash command submitted while a turn runs is queued, not dispatched or sent
// as steer text. It runs once the current turn finishes.
func TestTUI_RunningSlashQueues(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	setInput(m, "/clear")
	_, _ = m.submit()
	if len(m.queue) != 1 || m.queue[0].text != "/clear" {
		t.Fatalf("slash command should queue, got %+v", m.queue)
	}
	if m.a.Inbox.HasPending() {
		t.Error("slash command must not enqueue to inbox as steer text")
	}
}

func TestTUI_EscInterruptsRunningTurn(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	cancelled := false
	m.cancelTurn = func() { cancelled = true }
	m.queue = []pendingItem{{text: "later"}}

	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})

	if !cancelled {
		t.Error("Esc should cancel the running turn")
	}
	if len(m.queue) != 1 {
		t.Error("Esc must NOT clear the queue (design §9)")
	}
	if m.quit {
		t.Error("Esc must not quit")
	}
}

// Esc before the model produces any output (echo still pending) takes the turn
// back: the typed text returns to the input box and the deferred echo never
// reaches the scrollback.
func TestTUI_EscTakesBackBeforeOutput(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	cancelled := false
	m.cancelTurn = func() { cancelled = true }
	m.echoPending = userEchoStyle.Render("> ") + "fix the bug"
	m.echoRestore = "fix the bug"

	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})

	if !cancelled {
		t.Error("Esc should cancel the running turn")
	}
	if m.ta.Value() != "fix the bug" {
		t.Errorf("typed text should return to the input box, got %q", m.ta.Value())
	}
	if m.echoPending != "" {
		t.Error("the deferred echo should be dropped, not committed")
	}
	if len(m.printlnBuf) != 0 {
		t.Errorf("nothing should be flushed to the scrollback, got %v", m.printlnBuf)
	}
}

// A reasoning model's thinking is invisible in the terminal, so thinking deltas
// must not commit the echo. Esc during the thinking phase (still no visible
// output) must therefore still take the turn back and restore the typed text.
func TestTUI_EscTakesBackDuringThinking(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	cancelled := false
	m.cancelTurn = func() { cancelled = true }
	m.echoPending = userEchoStyle.Render("> ") + "fix the bug"
	m.echoRestore = "fix the bug"

	// The model thinks (invisible) before producing any answer text.
	m.handleEvent(agent.AgentEvent{Kind: agent.EventThinkingDelta, Text: "hmm, let me reason about this"})
	if m.echoPending == "" {
		t.Fatal("thinking must not commit the echo — take-back would be lost")
	}

	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})

	if !cancelled {
		t.Error("Esc should cancel the running turn")
	}
	if m.ta.Value() != "fix the bug" {
		t.Errorf("typed text should return to the input box, got %q", m.ta.Value())
	}
	if len(m.printlnBuf) != 0 {
		t.Errorf("nothing should be flushed to the scrollback, got %v", m.printlnBuf)
	}
}

// Title generation fires on receipt of the first message: with History still
// empty (the turn goroutine hasn't appended the input yet), titleCmd must
// build the throwaway prompt from the pending text alone — the old turn-end
// version returned nil until a turn had completed.
func TestTUI_TitleCmdFiresOnFirstMessage(t *testing.T) {
	m := newTestModel()
	m.cfg.noSave = false // newTestModel defaults to noSave, which gates titling
	m.cfg.session = agent.NewSession("m", "")

	c := m.titleCmd("fix the bug")
	if c == nil {
		t.Fatal("titleCmd = nil on first message, want a command (title on receipt)")
	}
	if !m.titlePending {
		t.Error("titlePending should be set while generation is in flight")
	}
	msg, ok := c().(titleMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want titleMsg", c())
	}
	if msg.text != "ok" { // stubSender's canned reply
		t.Errorf("title = %q, want %q", msg.text, "ok")
	}
}

// The 5-second guarantee's other half: when the throwaway provider call
// fails (bad model alias, rate limit, timeout), titleCmd still yields a
// non-empty titleMsg built from the pending message — the tab title always
// lands, never waits for a retry that may fail the same way. This is the
// same GenerateTitleOrSnippet mechanism the server uses.
func TestTUI_TitleCmdFallsBackToSnippet(t *testing.T) {
	isolateTestInputHistory()
	a := agent.New(&stubSender{err: fmt.Errorf("stub: provider down")}, "m")
	m := newTUIModel(replConfig{a: a})
	m.sink = &tuiSink{prog: &fakeProg{}}
	m.cfg.session = agent.NewSession("m", "")

	c := m.titleCmd("fix the flaky title test")
	if c == nil {
		t.Fatal("titleCmd = nil on first message, want a command")
	}
	msg, ok := c().(titleMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want titleMsg", c())
	}
	if msg.text != "fix the flaky…" {
		t.Errorf("title = %q, want the pending-message snippet %q", msg.text, "fix the flaky…")
	}
}

// An Esc take-back must leave no trace in the agent's history either: the
// interrupt keeps the input capped with a note (finishInterrupted), so
// handleTurnFinished has to strip that pair — otherwise the recalled text
// would silently survive in context as a ghost message and reappear when the
// session is resumed or opened in the Web UI.
func TestTUI_EscTakeBackStripsHistoryGhost(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	m.cancelTurn = func() {}
	m.echoPending = userEchoStyle.Render("> ") + "fix the bug"
	m.echoRestore = "fix the bug"

	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})

	// The turn goroutine winds down: finishInterrupted kept the input and
	// capped it with the interrupt note before turnFinishedMsg arrived.
	m.a.History.Append(agent.NewUserMessage("fix the bug"))
	m.a.History.Append(agent.NewAssistantMessage("[Interrupted by user.]"))
	_, _ = m.handleTurnFinished(context.Canceled)

	if n := m.a.History.Len(); n != 0 {
		t.Errorf("history len = %d, want 0 (take-back must leave no trace)", n)
	}
	if m.takeBackPending {
		t.Error("takeBackPending should be consumed by handleTurnFinished")
	}
}

// A plain interrupt (no take-back) keeps the interrupted input in history —
// handleTurnFinished must NOT strip it when the user didn't ask for a recall.
func TestTUI_PlainInterruptKeepsHistory(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	m.cancelTurn = func() {}
	// Echo already committed (output streamed) — Esc is a plain interrupt.
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})

	m.a.History.Append(agent.NewUserMessage("fix the bug"))
	m.a.History.Append(agent.NewAssistantMessage("[Interrupted by user.]"))
	_, _ = m.handleTurnFinished(context.Canceled)

	if n := m.a.History.Len(); n != 2 {
		t.Errorf("history len = %d, want 2 (plain interrupt keeps the input)", n)
	}
}

// Once output has streamed the echo is already committed (echoPending == ""), so
// Esc only interrupts — the last submitted message is NOT recalled into the box,
// even when it sits in history.
func TestTUI_EscAfterOutputJustInterrupts(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	cancelled := false
	m.cancelTurn = func() { cancelled = true }
	// echoPending already committed by the first output event.
	m.inputHistory = []string{"fix the bug"}

	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})

	if !cancelled {
		t.Error("Esc should still interrupt once output has started")
	}
	if m.ta.Value() != "" {
		t.Errorf("input box should stay empty after output, got %q", m.ta.Value())
	}
}

// Esc after output with empty history leaves the input box untouched.
func TestTUI_EscAfterOutputEmptyHistory(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	cancelled := false
	m.cancelTurn = func() { cancelled = true }

	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})

	if !cancelled {
		t.Error("Esc should interrupt")
	}
	if m.ta.Value() != "" {
		t.Errorf("input box should stay empty with no history, got %q", m.ta.Value())
	}
}

// The first streaming event promotes the deferred echo to the scrollback so the
// message lands just above the assistant reply.
func TestTUI_FirstOutputCommitsEcho(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	m.echoPending = userEchoStyle.Render("> ") + "hello"
	m.echoRestore = "hello"

	m.handleEvent(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "hi"})

	if m.echoPending != "" {
		t.Error("first output should commit (clear) the deferred echo")
	}
	// printlnBlock prefixes the echo with one blank separator line.
	if len(m.printlnBuf) == 0 || !strings.Contains(m.printlnBuf[len(m.printlnBuf)-1], "hello") {
		t.Fatalf("the echo should be queued to the scrollback, got %v", m.printlnBuf)
	}
}

// While the model streams a large tool argument (e.g. a big write_file body),
// the view must show a live byte counter so the wait reads as progress, not a
// freeze. EventToolStarted (args complete) clears the readout.
func TestTUI_ToolInputStreamProgress(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true

	m.handleEvent(agent.AgentEvent{Kind: agent.EventToolInputDelta, ToolID: "c1", ToolName: "write_file", InputDelta: strings.Repeat("x", 2048)})
	if m.toolStreamName != "write_file" || m.toolStreamBytes != 2048 {
		t.Fatalf("after first delta: name=%q bytes=%d", m.toolStreamName, m.toolStreamBytes)
	}
	out := m.View()
	if !strings.Contains(out, "Writing") || !strings.Contains(out, "2.0 KB") {
		t.Errorf("view should show a live write progress line; got:\n%s", out)
	}

	// A second fragment of the same call accumulates.
	m.handleEvent(agent.AgentEvent{Kind: agent.EventToolInputDelta, ToolID: "c1", ToolName: "write_file", InputDelta: strings.Repeat("x", 1024)})
	if m.toolStreamBytes != 3072 {
		t.Fatalf("bytes should accumulate within one call, got %d", m.toolStreamBytes)
	}

	// Args complete → tool dispatches → readout clears.
	m.handleEvent(agent.AgentEvent{Kind: agent.EventToolStarted, ToolID: "c1", ToolName: "write_file", Input: map[string]any{"path": "x.html"}})
	if m.toolStreamName != "" || m.toolStreamBytes != 0 {
		t.Errorf("EventToolStarted should clear the stream readout; name=%q bytes=%d", m.toolStreamName, m.toolStreamBytes)
	}
}

// A running terminal command's live output shows as a dimmed tail under the
// spinner (issue #1094) instead of vanishing until the done card commits.
func TestTUI_RunningToolShowsLiveTail(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true

	m.handleEvent(agent.AgentEvent{
		Kind: agent.EventToolStarted, ToolID: "c1", ToolName: "terminal",
		Input: map[string]any{"command": "make test"},
	})
	if m.running == nil {
		t.Fatal("EventToolStarted should set m.running")
	}

	m.handleEvent(agent.AgentEvent{Kind: agent.EventToolProgress, ToolID: "c1", Chunk: "compiling..."})
	m.handleEvent(agent.AgentEvent{Kind: agent.EventToolProgress, ToolID: "c1", Chunk: "running tests"})

	out := stripANSI(m.View())
	if !strings.Contains(out, "compiling...") || !strings.Contains(out, "running tests") {
		t.Errorf("view should show the streamed tail; got:\n%s", out)
	}
	if len(m.printlnBuf) != 0 {
		t.Errorf("rich TUI must not commit progress to the scrollback, got %v", m.printlnBuf)
	}

	// The tail is capped — older lines fall off once it exceeds the cap.
	for i := 0; i < runningTailMaxLines+2; i++ {
		m.handleEvent(agent.AgentEvent{Kind: agent.EventToolProgress, ToolID: "c1", Chunk: fmt.Sprintf("line-%d", i)})
	}
	if len(m.running.tail) != runningTailMaxLines {
		t.Fatalf("tail length = %d, want %d", len(m.running.tail), runningTailMaxLines)
	}
	if m.running.tail[len(m.running.tail)-1] != fmt.Sprintf("line-%d", runningTailMaxLines+1) {
		t.Errorf("tail should keep the most recent lines, got %v", m.running.tail)
	}

	// The done card clears the live indicator, and with it the tail.
	m.handleEvent(agent.AgentEvent{Kind: agent.EventToolDone, ToolID: "c1", Output: "ok"})
	if m.running != nil {
		t.Error("EventToolDone should clear m.running (and its tail)")
	}
}

// A command's streamed chunk is raw stdout/stderr — a stray \r or ESC must
// not reach the terminal unsanitized (the same control-byte injection
// surface #1101/#1104 closed for tool results/inputs), or it could overwrite
// the spinner line or spoof displayed text.
func TestTUI_RunningToolTailSanitizesControlBytes(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true

	m.handleEvent(agent.AgentEvent{
		Kind: agent.EventToolStarted, ToolID: "c1", ToolName: "terminal",
		Input: map[string]any{"command": "malicious"},
	})
	m.handleEvent(agent.AgentEvent{Kind: agent.EventToolProgress, ToolID: "c1", Chunk: "ok\rSPOOFED"})

	if m.running == nil || len(m.running.tail) != 1 {
		t.Fatalf("expected one tail line, got %+v", m.running)
	}
	if strings.ContainsRune(m.running.tail[0], '\r') {
		t.Errorf("raw \\r reached the live tail: %q", m.running.tail[0])
	}
	if !strings.Contains(m.running.tail[0], "SPOOFED") || !strings.Contains(m.running.tail[0], "ok") {
		t.Errorf("sanitized content should keep both halves visible: %q", m.running.tail[0])
	}
}

// --plain keeps its existing dense one-line-per-chunk behavior; progress
// commits straight to the scrollback instead of a live tail (design decision
// #8: the plain path never renders live/animated state).
func TestTUI_PlainModeProgressUnchanged(t *testing.T) {
	m := newTestModel()
	m.cfg.plain = true
	m.turnRunning = true

	m.handleEvent(agent.AgentEvent{
		Kind: agent.EventToolStarted, ToolID: "c1", ToolName: "terminal",
		Input: map[string]any{"command": "make test"},
	})
	m.handleEvent(agent.AgentEvent{Kind: agent.EventToolProgress, ToolID: "c1", Chunk: "compiling..."})

	if len(m.printlnBuf) == 0 || !strings.Contains(m.printlnBuf[len(m.printlnBuf)-1], "compiling...") {
		t.Fatalf("plain mode should commit progress to the scrollback, got %v", m.printlnBuf)
	}
}

// The reasoning trace stays out of the scrollback — only its size feeds the
// live activity line's "↓ ~N tokens" readout, so a long agentic turn doesn't
// fill the transcript with thinking text.
func TestTUI_ThinkingStaysOutOfScrollback(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true

	m.handleEvent(agent.AgentEvent{Kind: agent.EventThinkingDelta, Text: "step one\n"})
	m.handleEvent(agent.AgentEvent{Kind: agent.EventThinkingDelta, Text: strings.Repeat("x", 3999)})

	if joined := strings.Join(m.printlnBuf, "\n"); joined != "" {
		t.Fatalf("thinking must not reach the scrollback; got %q", joined)
	}
	if m.turnOutChars != 4008 {
		t.Errorf("turnOutChars = %d, want 4008", m.turnOutChars)
	}
	// The wait-on-model activity line shows the running token estimate (chars/4).
	out := m.View()
	if !strings.Contains(out, "↓ ~1.0k tokens") {
		t.Errorf("view should show the output-token readout; got:\n%s", out)
	}
}

// Before any token streams back the wait line shows the uplink ↑ (request still
// going up); once tokens arrive it flips to the downlink ↓ with a count.
func TestTUI_ThinkingArrowUplinkThenDownlink(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true

	// Uplink: nothing received yet — up arrow, no count, no downlink arrow.
	out := m.View()
	if !strings.Contains(out, "· ↑") {
		t.Errorf("uplink wait line should show ↑; got:\n%s", out)
	}
	if strings.Contains(out, "↓") {
		t.Errorf("no downlink ↓ before any token; got:\n%s", out)
	}

	// Downlink: a reasoning delta arrives — flip to ↓ with the token readout.
	m.handleEvent(agent.AgentEvent{Kind: agent.EventThinkingDelta, Text: strings.Repeat("x", 400)})
	out = m.View()
	if !strings.Contains(out, "↓ ~") {
		t.Errorf("downlink wait line should show ↓ with a count; got:\n%s", out)
	}
}

// After a real reasoning phase the first answer delta is held briefly (the
// hand-off sprint) instead of streaming immediately; flushSprint releases it.
func TestTUI_AnswerSprintHoldsThenReleases(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	m.assistantFirstBlock = true

	// A reasoning phase ≥ the sprint threshold precedes the answer.
	m.handleEvent(agent.AgentEvent{Kind: agent.EventThinkingDelta, Text: strings.Repeat("x", answerSprintMinChars)})

	// First answer delta: held, not appended to the live partial.
	m.handleEvent(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "Here is the answer."})
	if !m.answerSprint {
		t.Fatal("first answer delta after a reasoning phase should start the sprint")
	}
	if m.partial.Len() != 0 {
		t.Errorf("answer text must be held during the sprint, not streamed; partial=%q", m.partial.String())
	}

	// Release: the sprint ends and the held text reaches the live partial.
	m.flushSprint()
	if m.answerSprint {
		t.Error("flushSprint should end the sprint")
	}
	if m.partial.Len() == 0 && len(m.printlnBuf) == 0 {
		t.Error("released answer text should reach the partial or scrollback")
	}
}

// A direct reply with no meaningful reasoning phase must not be held back — the
// sprint only kicks in after a real uplink/reasoning wait, so quick answers add
// no latency.
func TestTUI_QuickReplySkipsSprint(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	m.assistantFirstBlock = true

	m.handleEvent(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "hi"})
	if m.answerSprint {
		t.Error("a direct reply with no reasoning phase must not sprint")
	}
}

// A non-text event arriving mid-sprint (e.g. a tool call) releases the held
// answer text first, so output commits in the right order.
func TestTUI_SprintFlushedByNonTextEvent(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	m.assistantFirstBlock = true

	m.handleEvent(agent.AgentEvent{Kind: agent.EventThinkingDelta, Text: strings.Repeat("x", answerSprintMinChars)})
	m.handleEvent(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "partial answer"})
	if !m.answerSprint {
		t.Fatal("sprint should be active")
	}
	m.handleEvent(agent.AgentEvent{Kind: agent.EventToolStarted, ToolID: "c1", ToolName: "mcp_thing", Input: map[string]any{"q": "x"}})
	if m.answerSprint {
		t.Error("a non-text event should flush and end the sprint")
	}
}

// A new turn resets the output-token readout.
func TestTUI_TurnStartResetsOutChars(t *testing.T) {
	m := newTestModel()
	m.turnOutChars = 999
	_ = m.startTurnEcho("hi", "hi")
	if m.turnOutChars != 0 {
		t.Errorf("turnOutChars after turn start = %d, want 0", m.turnOutChars)
	}
}

// Non-card tools (MCP tools, sub_agent, …) commit a single header-style status
// line on completion — no started line, no progress lines.
func TestTUI_NonCardToolOneLine(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true

	m.handleEvent(agent.AgentEvent{Kind: agent.EventToolStarted, ToolID: "c1", ToolName: "mcp_thing", Input: map[string]any{"q": "x"}})
	if len(m.printlnBuf) != 0 {
		t.Fatalf("started must not print; got %v", m.printlnBuf)
	}
	if m.running == nil || m.running.verb != "mcp_thing" {
		t.Fatalf("started should set the live indicator; got %+v", m.running)
	}
	m.handleEvent(agent.AgentEvent{Kind: agent.EventToolProgress, ToolID: "c1", ToolName: "mcp_thing", Chunk: "partial"})
	if len(m.printlnBuf) != 0 {
		t.Fatalf("progress must not print; got %v", m.printlnBuf)
	}
	m.handleEvent(agent.AgentEvent{Kind: agent.EventToolDone, ToolID: "c1", ToolName: "mcp_thing", Output: "done"})

	joined := stripANSI(strings.Join(m.printlnBuf, "\n"))
	if !strings.Contains(joined, "● mcp_thing(q=x)") {
		t.Errorf("done should commit one status line; got %q", joined)
	}
	if m.running != nil {
		t.Error("done should clear the live indicator")
	}
}

// Block-level scrollback items are separated by exactly one blank line; the
// separator is not duplicated when the previous line is already blank.
func TestTUI_PrintlnBlockSeparation(t *testing.T) {
	m := newTestModel()
	m.printlnBlock("first")
	m.printlnBlock("second")
	want := []string{"", "first", "", "second"}
	if len(m.printlnBuf) != len(want) {
		t.Fatalf("printlnBuf = %q, want %q", m.printlnBuf, want)
	}
	for i, w := range want {
		if m.printlnBuf[i] != w {
			t.Fatalf("printlnBuf[%d] = %q, want %q", i, m.printlnBuf[i], w)
		}
	}
}

// Resuming a session replays prior turns into the scrollback exactly as they
// appeared live: prompts and replies verbatim, and tool calls rendered through
// the same renderToolOutcome path — so a card tool (terminal) shows its rich
// card with the real output, not a collapsed one-liner.
func TestTUI_ReplayHistoryLines(t *testing.T) {
	m := newTestModel()
	m.width = 80
	h := m.a.History
	h.Append(agent.NewUserMessage("hello there"))
	h.Append(agent.NewToolUseMessage([]agent.ContentBlock{
		agent.NewTextBlock("let me check"),
		agent.NewToolUseBlock("c1", "terminal", map[string]any{"command": "ls"}),
	}))
	h.Append(agent.NewToolResultMessage([]agent.ContentBlock{
		agent.NewToolResultBlock("c1", "file1\nfile2", false),
	}))
	h.Append(agent.NewAssistantMessage("here are the files"))

	joined := stripANSI(strings.Join(m.replayHistoryLines(), "\n"))

	for _, want := range []string{"hello there", "let me check", "here are the files", "Run(ls)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("replay missing %q; got:\n%s", want, joined)
		}
	}
	// The replayed card carries the real tool output, same as the live card.
	if !strings.Contains(joined, "file1") {
		t.Errorf("replay should render the live card with raw output; got:\n%s", joined)
	}
	// No special-cased "resumed" marker — replay is indistinguishable from live.
	if strings.Contains(joined, "resumed") {
		t.Errorf("replay should not append a special marker; got:\n%s", joined)
	}
}

// recentToolCalls pairs tool_use with tool_result across history, in
// chronological order, and skips a call still awaiting its result.
func TestTUI_RecentToolCalls(t *testing.T) {
	m := newTestModel()
	h := m.a.History
	h.Append(agent.NewToolUseMessage([]agent.ContentBlock{
		agent.NewToolUseBlock("c1", "terminal", map[string]any{"command": "one"}),
	}))
	h.Append(agent.NewToolResultMessage([]agent.ContentBlock{
		agent.NewToolResultBlock("c1", "out1", false),
	}))
	h.Append(agent.NewToolUseMessage([]agent.ContentBlock{
		agent.NewToolUseBlock("c2", "terminal", map[string]any{"command": "two"}),
	}))
	h.Append(agent.NewToolResultMessage([]agent.ContentBlock{
		agent.NewToolResultBlock("c2", "out2", false),
	}))
	// c3 has no matching tool_result yet — still running, must be skipped.
	h.Append(agent.NewToolUseMessage([]agent.ContentBlock{
		agent.NewToolUseBlock("c3", "terminal", map[string]any{"command": "three"}),
	}))

	all := m.recentToolCalls(10)
	if len(all) != 2 {
		t.Fatalf("recentToolCalls(10) = %d calls, want 2 (mid-flight call skipped); got %+v", len(all), all)
	}
	if all[0].result != "out1" || all[1].result != "out2" {
		t.Fatalf("recentToolCalls should preserve chronological order, got %+v", all)
	}

	last := m.recentToolCalls(1)
	if len(last) != 1 || last[0].result != "out2" {
		t.Fatalf("recentToolCalls(1) should return the most recent call, got %+v", last)
	}
}

// /transcript re-prints the last tool call with its full, uncapped output —
// the in-terminal fallback for #1093's folded-output recovery.
func TestTUI_DispatchTranscript(t *testing.T) {
	setSpillHome(t)
	m := newTestModel()
	m.width = 80
	h := m.a.History
	long := strings.TrimSuffix(strings.Repeat("row\n", 12), "\n")
	h.Append(agent.NewToolUseMessage([]agent.ContentBlock{
		agent.NewToolUseBlock("c1", "terminal", map[string]any{"command": "ls"}),
	}))
	h.Append(agent.NewToolResultMessage([]agent.ContentBlock{
		agent.NewToolResultBlock("c1", long, false),
	}))

	_, _ = m.dispatchTranscript("")
	got := stripANSI(strings.Join(m.printlnBuf, "\n"))
	if strings.Contains(got, "…") {
		t.Errorf("/transcript should show uncapped output, got:\n%s", got)
	}
	if n := strings.Count(got, "row"); n != 12 {
		t.Errorf("/transcript should show all 12 lines, got %d occurrences:\n%s", n, got)
	}
}

// /transcript on a NON-CARD tool's error must also show the full text, not
// the same one-line-truncated errText the live status row already showed —
// otherwise "uncapped" is a lie for exactly the case (a long multi-line
// error) it exists to fix.
func TestTUI_DispatchTranscript_NonCardToolErrorUncapped(t *testing.T) {
	m := newTestModel()
	h := m.a.History
	long := strings.Repeat("stack frame\n", 10) + "fatal error"
	h.Append(agent.NewToolUseMessage([]agent.ContentBlock{
		agent.NewToolUseBlock("c1", "mcp_thing", map[string]any{"q": "x"}),
	}))
	h.Append(agent.NewToolResultMessage([]agent.ContentBlock{
		agent.NewToolResultBlock("c1", long, true),
	}))

	_, _ = m.dispatchTranscript("")
	got := stripANSI(strings.Join(m.printlnBuf, "\n"))
	if n := strings.Count(got, "stack frame"); n != 10 {
		t.Errorf("/transcript should show every line of a non-card tool's error, got %d occurrences:\n%s", n, got)
	}
	if !strings.Contains(got, "fatal error") {
		t.Errorf("/transcript should include the error's last line too:\n%s", got)
	}
}

// No completed tool calls yet — /transcript reports that instead of a
// misleading empty print.
func TestTUI_DispatchTranscript_NoCalls(t *testing.T) {
	m := newTestModel()
	if _, _ = m.dispatchTranscript(""); len(m.printlnBuf) != 1 {
		t.Fatalf("dispatchTranscript should print a notice, got %v", m.printlnBuf)
	}
	if !strings.Contains(m.printlnBuf[0], "no completed tool calls") {
		t.Errorf("expected a 'no tool calls' notice, got %q", m.printlnBuf[0])
	}
}

// A non-numeric or non-positive count argument is rejected with a usage hint
// rather than silently falling back to some default.
func TestTUI_DispatchTranscript_BadArg(t *testing.T) {
	m := newTestModel()
	if _, _ = m.dispatchTranscript("nope"); len(m.printlnBuf) != 1 || !strings.Contains(m.printlnBuf[0], "Usage") {
		t.Errorf("bad count should print usage, got %v", m.printlnBuf)
	}
	m.printlnBuf = nil
	if _, _ = m.dispatchTranscript("0"); len(m.printlnBuf) != 1 || !strings.Contains(m.printlnBuf[0], "Usage") {
		t.Errorf("zero count should print usage, got %v", m.printlnBuf)
	}
}

// stripANSI removes ANSI SGR escape sequences so assertions can match the
// underlying text (glamour splits words across color spans).
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func TestTUI_ReplayHistoryLines_FreshSessionEmpty(t *testing.T) {
	m := newTestModel()
	if lines := m.replayHistoryLines(); lines != nil {
		t.Errorf("a fresh session should replay nothing; got %v", lines)
	}
}

// A resumed session with more than replayMaxTurns turns only replays the
// most recent ones, plus a single omission line for the rest — otherwise a
// few-hundred-turn session floods the terminal on resume (#1097).
func TestTUI_ReplayHistoryLines_CapsOldTurns(t *testing.T) {
	m := newTestModel()
	m.width = 80
	h := m.a.History
	total := replayMaxTurns + 5
	for i := 0; i < total; i++ {
		h.Append(agent.NewUserMessage(fmt.Sprintf("turn %d", i)))
		h.Append(agent.NewAssistantMessage(fmt.Sprintf("reply %d", i)))
	}

	joined := stripANSI(strings.Join(m.replayHistoryLines(), "\n"))

	if !strings.Contains(joined, "earlier history omitted (5 turns)") {
		t.Errorf("missing omission line for the 5 dropped turns; got:\n%s", joined)
	}
	// The oldest turns are gone...
	if strings.Contains(joined, "turn 0") {
		t.Errorf("oldest turn should have been dropped; got:\n%s", joined)
	}
	// ...but the most recent replayMaxTurns are kept.
	if !strings.Contains(joined, fmt.Sprintf("turn %d", total-1)) {
		t.Errorf("newest turn should be replayed; got:\n%s", joined)
	}
	if !strings.Contains(joined, fmt.Sprintf("turn %d", total-replayMaxTurns)) {
		t.Errorf("first kept turn should be replayed; got:\n%s", joined)
	}
}

// TestTUI_ReplayHistoryLines_SingularOmission checks the omission line's
// singular/plural wording at the exact cap+1 boundary ("1 turn", not "1 turns").
func TestTUI_ReplayHistoryLines_SingularOmission(t *testing.T) {
	m := newTestModel()
	m.width = 80
	h := m.a.History
	total := replayMaxTurns + 1
	for i := 0; i < total; i++ {
		h.Append(agent.NewUserMessage(fmt.Sprintf("turn %d", i)))
		h.Append(agent.NewAssistantMessage(fmt.Sprintf("reply %d", i)))
	}

	joined := stripANSI(strings.Join(m.replayHistoryLines(), "\n"))
	if !strings.Contains(joined, "earlier history omitted (1 turn)") {
		t.Errorf("want singular '1 turn' in omission line; got:\n%s", joined)
	}
	if strings.Contains(joined, "1 turns") {
		t.Errorf("wrong plural for a single omitted turn; got:\n%s", joined)
	}
}

// A failed tool call replays as its live error card, carrying the real error
// message (the regression: the old collapsed line dropped it).
func TestTUI_ReplayHistoryLines_ToolError(t *testing.T) {
	m := newTestModel()
	m.width = 80
	h := m.a.History
	h.Append(agent.NewUserMessage("run it"))
	h.Append(agent.NewToolUseMessage([]agent.ContentBlock{
		agent.NewToolUseBlock("c1", "terminal", map[string]any{"command": "boom"}),
	}))
	h.Append(agent.NewToolResultMessage([]agent.ContentBlock{
		agent.NewToolResultBlock("c1", "exit 1", true),
	}))

	joined := stripANSI(strings.Join(m.replayHistoryLines(), "\n"))
	if !strings.Contains(joined, "exit 1") {
		t.Errorf("failed tool should replay its error message; got:\n%s", joined)
	}
}

func TestTUI_CtrlDQuits(t *testing.T) {
	m := newTestModel()

	// First press arms but must not quit.
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	if m.quit || cmd != nil {
		t.Fatal("first Ctrl+D should arm, not quit")
	}
	if !m.quitArmed {
		t.Fatal("first Ctrl+D should arm the quit confirmation")
	}

	// Second consecutive press quits.
	_, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	if !m.quit || cmd == nil {
		t.Error("second Ctrl+D should set quit and return a quit cmd")
	}
}

func TestTUI_CtrlCQuitsWhenIdle(t *testing.T) {
	m := newTestModel()

	// First idle Ctrl+C arms but must not quit.
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.quit || cmd != nil {
		t.Fatal("first idle Ctrl+C should arm, not quit")
	}
	if !m.quitArmed {
		t.Fatal("first idle Ctrl+C should arm the quit confirmation")
	}

	// Second consecutive press quits.
	_, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.quit || cmd == nil {
		t.Error("second idle Ctrl+C should set quit and return a quit cmd")
	}
}

func TestTUI_CtrlCInterruptsWithoutArmingQuit(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true

	// While a turn runs, Ctrl+C interrupts and must never arm a quit.
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.quit || cmd != nil {
		t.Fatal("running-turn Ctrl+C should interrupt, not quit")
	}
	if m.quitArmed {
		t.Error("running-turn Ctrl+C must not arm the quit confirmation")
	}
}

func TestTUI_CtrlDAndCShareQuitConfirmation(t *testing.T) {
	m := newTestModel()

	// Ctrl+D arms; a following idle Ctrl+C confirms the shared quit.
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.quit || cmd == nil {
		t.Error("Ctrl+C after Ctrl+D should confirm the shared quit")
	}
}

func TestTUI_CtrlCArmsThenCtrlDConfirms(t *testing.T) {
	m := newTestModel()

	// Reverse of the shared-confirmation test: idle Ctrl+C arms, Ctrl+D confirms.
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	if !m.quit || cmd == nil {
		t.Error("Ctrl+D after idle Ctrl+C should confirm the shared quit")
	}
}

func TestTUI_QuitDisarmedByOtherKey(t *testing.T) {
	m := newTestModel()

	// Arm with one Ctrl+D, then press another key — the confirmation resets.
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if m.quitArmed {
		t.Fatal("a non-quit key should disarm the quit confirmation")
	}

	// A lone Ctrl+D after the reset must arm again, not quit.
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	if m.quit || cmd != nil {
		t.Error("Ctrl+D after a disarm should re-arm, not quit")
	}
}

func TestTUI_QuitDisarmedByEarlyReturnKey(t *testing.T) {
	m := newTestModel()

	// Arm, then press a key that the completion menu consumes with an early
	// return (before the main key switch). The top-of-handleKey guard must still
	// disarm, otherwise the confirmation would leak past menu navigation.
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	m.complItems = []complItem{{"/help", ""}, {"/model", ""}}
	m.complIdx = 0
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.quitArmed {
		t.Error("an early-return completion key should still disarm the quit confirmation")
	}
}

func TestTUI_CtrlDArmedThenRunningCtrlCInterrupts(t *testing.T) {
	m := newTestModel()

	// Ctrl+D arms while idle; then a turn starts and Ctrl+C arrives. It must
	// interrupt and disarm, never confirming the pending quit.
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	m.turnRunning = true
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.quit || cmd != nil {
		t.Fatal("running-turn Ctrl+C must interrupt, not confirm an armed quit")
	}
	if m.quitArmed {
		t.Error("running-turn Ctrl+C must disarm the pending quit confirmation")
	}
}

func TestTUI_TurnFinishedDequeuesNext(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	m.queue = []pendingItem{{text: "next task"}}

	_, _ = m.handleTurnFinished(nil)

	// Dequeued and a new turn started.
	if !m.turnRunning {
		t.Fatal("a queued item should start the next turn")
	}
	if len(m.queue) != 0 {
		t.Errorf("queue should be drained, got %+v", m.queue)
	}
}

// A slash command queued mid-turn is dispatched once the turn finishes, not run
// as a plain user message.
func TestTUI_TurnFinishedDispatchesQueuedSlash(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	m.queue = []pendingItem{{text: "/clear"}}

	_, _ = m.handleTurnFinished(nil)

	// /clear clears history and leaves the queue empty.
	if m.a.History.Len() != 0 {
		t.Errorf("queued /clear should have cleared history; len=%d", m.a.History.Len())
	}
	if len(m.queue) != 0 {
		t.Errorf("queue should be drained, got %+v", m.queue)
	}
}

func TestTUI_DegradedInboxRunsBeforeQueue(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	m.a.Inbox.Enqueue("degraded inbox") // never drained during turn → pending at turn end
	m.queue = []pendingItem{{text: "queued"}}

	_, _ = m.handleTurnFinished(nil)

	// The degraded inbox message is prepended, so it's the one that starts running and
	// "queued" remains in the queue.
	if len(m.queue) != 1 || m.queue[0].text != "queued" {
		t.Fatalf("queue after dequeue = %+v, want [queued]", m.queue)
	}
	if m.a.Inbox.HasPending() {
		t.Error("inbox should have been drained into the queue")
	}
}

func TestTUI_PermissionModal(t *testing.T) {
	m := newTestModel()
	resp := make(chan UserResponse, 1)
	m.openModal(askMsg{prompt: UserPrompt{Kind: KindPermission, ToolName: "terminal"}, resp: resp})
	if m.modal == nil {
		t.Fatal("modal should be open")
	}

	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	got := <-resp
	if !got.Allow || !got.Always {
		t.Errorf("'a' should allow+always, got %+v", got)
	}
	if m.modal != nil {
		t.Error("modal should close after answering")
	}
}

// A second Ask arriving while a modal is open (concurrent sub-agents can each
// prompt at once) is queued, not dropped: the first prompt stays on screen, the
// second opens once the first is answered, and neither Ask goroutine is
// stranded.
func TestTUI_ConcurrentAsksQueue(t *testing.T) {
	m := newTestModel()
	resp1 := make(chan UserResponse, 1)
	resp2 := make(chan UserResponse, 1)
	m.openModal(askMsg{prompt: UserPrompt{Kind: KindPermission, ToolName: "terminal"}, resp: resp1})
	m.openModal(askMsg{prompt: UserPrompt{Kind: KindPermission, ToolName: "write_file"}, resp: resp2})

	if m.modal == nil || m.modal.prompt.ToolName != "terminal" {
		t.Fatalf("first prompt should be showing, got %+v", m.modal)
	}
	if len(m.modalQueue) != 1 {
		t.Fatalf("second prompt should be queued, queue len = %d", len(m.modalQueue))
	}

	// Answer the first: its resp fires and the queued second opens.
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if got := <-resp1; !got.Allow {
		t.Errorf("first prompt should have been allowed, got %+v", got)
	}
	if m.modal == nil || m.modal.prompt.ToolName != "write_file" {
		t.Fatalf("queued prompt should now be showing, got %+v", m.modal)
	}
	if len(m.modalQueue) != 0 {
		t.Errorf("queue should be empty after popping, len = %d", len(m.modalQueue))
	}

	// Answer the second.
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if got := <-resp2; got.Allow {
		t.Errorf("second prompt should have been denied, got %+v", got)
	}
	if m.modal != nil {
		t.Error("no modal should remain after both answered")
	}
}

// A background sub-agent's permission prompt outlives the turn that spawned it
// (its context is detached from the turn), so a finishing turn must NOT clear an
// open/queued modal — doing so would strand that sub-agent's Ask goroutine.
func TestTUI_TurnFinishedKeepsBackgroundModal(t *testing.T) {
	m := newTestModel()
	m.openModal(askMsg{prompt: UserPrompt{Kind: KindPermission, ToolName: "terminal"}, resp: make(chan UserResponse, 1)})
	m.openModal(askMsg{prompt: UserPrompt{Kind: KindPermission, ToolName: "write_file"}, resp: make(chan UserResponse, 1)})
	m.turnRunning = true

	m.handleTurnFinished(nil)

	if m.modal == nil {
		t.Error("modal must survive turn finish (may belong to a background sub-agent)")
	}
	if len(m.modalQueue) != 1 {
		t.Errorf("queued modal must survive turn finish, len = %d", len(m.modalQueue))
	}
}

func TestTUI_PermissionModalEscDenies(t *testing.T) {
	m := newTestModel()
	resp := make(chan UserResponse, 1)
	m.openModal(askMsg{prompt: UserPrompt{Kind: KindPermission, ToolName: "terminal"}, resp: resp})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyEsc})
	if got := <-resp; got.Allow {
		t.Errorf("Esc should deny, got %+v", got)
	}
}

func TestTUI_QuestionModalSelect(t *testing.T) {
	m := newTestModel()
	resp := make(chan UserResponse, 1)
	m.openModal(askMsg{prompt: UserPrompt{
		Kind:     KindQuestion,
		Question: "pick",
		Options:  []string{"alpha", "beta"},
	}, resp: resp})

	// Move to "beta" and confirm.
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})

	got := <-resp
	if len(got.Choices) != 1 || got.Choices[0] != "beta" {
		t.Errorf("selection = %+v, want [beta]", got.Choices)
	}
}

func TestTUI_QuestionModalMultiSelect(t *testing.T) {
	m := newTestModel()
	resp := make(chan UserResponse, 1)
	m.openModal(askMsg{prompt: UserPrompt{
		Kind:        KindQuestion,
		Question:    "pick many",
		Options:     []string{"a", "b", "c"},
		MultiSelect: true,
	}, resp: resp})

	// Toggle index 0, move to 2, toggle, confirm.
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeySpace})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeySpace})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})

	got := <-resp
	if len(got.Choices) != 2 || got.Choices[0] != "a" || got.Choices[1] != "c" {
		t.Errorf("multi-select = %+v, want [a c]", got.Choices)
	}
}

func TestTUI_QuestionModalOtherFreeText(t *testing.T) {
	m := newTestModel()
	resp := make(chan UserResponse, 1)
	m.openModal(askMsg{prompt: UserPrompt{
		Kind:     KindQuestion,
		Question: "pick",
		Options:  []string{"alpha", "beta"},
	}, resp: resp})

	// Move to "Other" and confirm to enter free-text mode.
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.modal.otherActive {
		t.Fatal("selecting Other should activate free-text input")
	}

	// Type "custom", backspace once, then confirm.
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c', 'u', 's', 't', 'o', 'm'}})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyBackspace})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})

	got := <-resp
	if got.Custom != "custo" {
		t.Errorf("custom = %q, want custo", got.Custom)
	}
	if got.Cancelled {
		t.Error("should not be cancelled after confirming non-empty text")
	}
}

// The "Other" free-text field is a real textinput.Model, not an
// append/backspace-only buffer — it must support moving the cursor and
// inserting/deleting in the middle of the text (#1097).
func TestTUI_QuestionModalOtherCursorMovement(t *testing.T) {
	m := newTestModel()
	resp := make(chan UserResponse, 1)
	m.openModal(askMsg{prompt: UserPrompt{
		Kind:     KindQuestion,
		Question: "pick",
		Options:  []string{"alpha"},
	}, resp: resp})

	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.modal.otherActive {
		t.Fatal("selecting Other should activate free-text input")
	}

	// Type "acd", move left twice (cursor between 'a' and 'c'), insert 'b'.
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a', 'c', 'd'}})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyLeft})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyLeft})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})

	got := <-resp
	if got.Custom != "abcd" {
		t.Errorf("custom = %q, want abcd (cursor movement should allow mid-string insert)", got.Custom)
	}
}

func TestTUI_QuestionModalOtherEmptyCancels(t *testing.T) {
	m := newTestModel()
	resp := make(chan UserResponse, 1)
	m.openModal(askMsg{prompt: UserPrompt{
		Kind:     KindQuestion,
		Question: "pick",
		Options:  []string{"alpha"},
	}, resp: resp})

	// Select Other (Enter), then confirm empty input (Enter) to cancel.
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})

	got := <-resp
	if !got.Cancelled {
		t.Errorf("empty Other should cancel, got %+v", got)
	}
}

func TestTUI_QuestionModalOtherEscCancels(t *testing.T) {
	m := newTestModel()
	resp := make(chan UserResponse, 1)
	m.openModal(askMsg{prompt: UserPrompt{
		Kind:     KindQuestion,
		Question: "pick",
		Options:  []string{"alpha"},
	}, resp: resp})

	// Select Other (Enter), type something, then press Esc.
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyEsc})

	got := <-resp
	if !got.Cancelled {
		t.Errorf("Esc in Other input should cancel, got %+v", got)
	}
}

func TestTUI_QuestionModalMultiSelectOther(t *testing.T) {
	m := newTestModel()
	resp := make(chan UserResponse, 1)
	m.openModal(askMsg{prompt: UserPrompt{
		Kind:        KindQuestion,
		Question:    "pick",
		Options:     []string{"alpha", "beta"},
		MultiSelect: true,
	}, resp: resp})

	// Toggle Other and confirm to enter free-text mode.
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeySpace})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.modal.otherActive {
		t.Fatal("selecting Other in multi-select should activate free-text input")
	}

	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m', 'y'}})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})

	got := <-resp
	if got.Custom != "my" {
		t.Errorf("custom = %q, want my", got.Custom)
	}
}

func TestTUI_AppendText_BlockBufferingNonPlain(t *testing.T) {
	m := newTestModel() // cfg.plain == false → markdown block buffering
	m.appendText("hello ")
	m.appendText("world\nnext")
	// No blank-line block boundary yet, so the whole in-progress block stays live.
	if got := m.partial.String(); got != "hello world\nnext" {
		t.Errorf("partial = %q, want the full in-progress block", got)
	}
	// A blank line closes the block; only the next block remains live.
	m.appendText("\n\nsecond para start")
	if got := m.partial.String(); got != "second para start" {
		t.Errorf("partial = %q, want 'second para start'", got)
	}
}

func TestTUI_AppendText_PlainLineFlush(t *testing.T) {
	m := newTestModel()
	m.cfg.plain = true // --plain keeps the line-by-line raw flush
	m.appendText("hello ")
	m.appendText("world\nnext")
	if got := m.partial.String(); got != "next" {
		t.Errorf("partial = %q, want 'next' (completed line flushed)", got)
	}
}

func TestTUI_InputHistoryRecall(t *testing.T) {
	m := newTestModel()
	m.inputHistory = []string{"first", "second", "third"}

	// Up recalls the most recent line.
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	if got := m.ta.Value(); got != "third" {
		t.Errorf("input after first Up = %q, want third", got)
	}
	if m.inputHistoryIdx != 0 {
		t.Errorf("history idx = %d, want 0", m.inputHistoryIdx)
	}

	// Up again goes further back.
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	if got := m.ta.Value(); got != "second" {
		t.Errorf("input after second Up = %q, want second", got)
	}

	// Down moves forward in history.
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if got := m.ta.Value(); got != "third" {
		t.Errorf("input after Down = %q, want third", got)
	}

	// Down past the newest restores empty input.
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.ta.Value() != "" {
		t.Errorf("input after Down past newest = %q, want empty", m.ta.Value())
	}
	if m.inputHistoryIdx != -1 {
		t.Errorf("history idx = %d, want -1", m.inputHistoryIdx)
	}
}

func TestTUI_SubmitSavesToHistory(t *testing.T) {
	m := newTestModel()
	setInput(m, "hello")
	_, _ = m.submit()
	if len(m.inputHistory) != 1 || m.inputHistory[0] != "hello" {
		t.Errorf("history after submit = %v, want [hello]", m.inputHistory)
	}

	// Duplicate consecutive line is deduped.
	setInput(m, "hello")
	_, _ = m.submit()
	if len(m.inputHistory) != 1 {
		t.Errorf("history after duplicate = %v, want still 1 entry", m.inputHistory)
	}

	// Different line is added.
	setInput(m, "world")
	_, _ = m.submit()
	if len(m.inputHistory) != 2 || m.inputHistory[1] != "world" {
		t.Errorf("history after new line = %v, want [hello world]", m.inputHistory)
	}
}

// The terminal never renders the reasoning trace — that display is web-only.
// A thinking delta must not appear in the live View (nor the scrollback); it
// only feeds the activity line's output-token readout.
func TestTUI_ThinkingNeverShownInTerminal(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true

	m.handleEvent(agent.AgentEvent{Kind: agent.EventThinkingDelta, Text: "let me think"})

	if joined := strings.Join(m.printlnBuf, "\n"); joined != "" {
		t.Fatalf("thinking must not reach the scrollback; got %q", joined)
	}
	if out := m.View(); strings.Contains(out, "let me think") {
		t.Errorf("view must NOT contain thinking text; got:\n%s", out)
	}
	if m.turnOutChars != 12 {
		t.Errorf("turnOutChars = %d, want 12", m.turnOutChars)
	}
}

// Shift+Tab must cycle through all three permission modes, including strict —
// previously it only toggled interactive/auto, leaving strict unreachable
// from the keyboard (#1097).
func TestTUI_ShiftTabCyclesAllPermissionModes(t *testing.T) {
	eng, err := permission.New("", t.TempDir(), permission.ModeInteractive)
	if err != nil {
		t.Fatalf("permission.New: %v", err)
	}
	m := newTestModel()
	m.cfg.permEngine = eng

	want := []permission.Mode{
		permission.ModeAutoApprove,
		permission.ModeStrict,
		permission.ModeInteractive,
	}
	for _, w := range want {
		m2, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyShiftTab})
		m = m2.(*tuiModel)
		if got := eng.GetMode(); got != w {
			t.Errorf("mode = %q, want %q", got, w)
		}
	}
}

// TestTUI_InputFold_TabToggles tests that Tab toggles the folded state
// when there are >= 5 lines of input.
func TestTUI_InputFold_TabToggles(t *testing.T) {
	m := newTestModel()
	multiLine := "line1\nline2\nline3\nline4\nline5"
	setInput(m, multiLine)

	// Tab should fold the input
	m2, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = m2.(*tuiModel)
	if !m.inputFolded {
		t.Fatal("Tab should fold multi-line input")
	}
	if m.foldedFullText != multiLine {
		t.Errorf("foldedFullText = %q, want %q", m.foldedFullText, multiLine)
	}
	if m.inputFoldedLines != 5 {
		t.Errorf("inputFoldedLines = %d, want 5", m.inputFoldedLines)
	}

	// View should show the folded placeholder
	view := m.View()
	if !strings.Contains(view, "5 lines pasted") {
		t.Errorf("view should show folded placeholder, got:\n%s", view)
	}

	// Tab again should expand
	m2, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = m2.(*tuiModel)
	if m.inputFolded {
		t.Error("Tab should expand folded input")
	}
	if m.ta.Value() != multiLine {
		t.Errorf("textarea value after expand = %q, want %q", m.ta.Value(), multiLine)
	}
	if m.foldedFullText != "" {
		t.Error("foldedFullText should be cleared after expand")
	}
}

// TestTUI_InputFold_NoStrayTab guards against Tab leaking a literal tab
// character into foldedFullText: the fold check must run before the
// keypress reaches the textarea, not after (#1097).
func TestTUI_InputFold_NoStrayTab(t *testing.T) {
	m := newTestModel()
	multiLine := "line1\nline2\nline3\nline4\nline5"
	setInput(m, multiLine)

	m2, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = m2.(*tuiModel)
	if !m.inputFolded {
		t.Fatal("Tab should fold multi-line input")
	}
	if strings.Contains(m.foldedFullText, "\t") {
		t.Errorf("foldedFullText contains a stray tab: %q", m.foldedFullText)
	}
	if m.foldedFullText != multiLine {
		t.Errorf("foldedFullText = %q, want %q", m.foldedFullText, multiLine)
	}
}

// TestTUI_InputFold_SubmitUsesFullText tests that submit uses the full text
// when input is folded.
func TestTUI_InputFold_SubmitUsesFullText(t *testing.T) {
	m := newTestModel()
	multiLine := "hello\nworld\nfoo\nbar\nbaz"
	setInput(m, multiLine)

	// Fold the input
	m2, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = m2.(*tuiModel)
	if !m.inputFolded {
		t.Fatal("input should be folded")
	}

	// Submit should use the full text
	_, _ = m.submit()
	if !m.turnRunning {
		t.Fatal("submit should start a turn")
	}
	// The history should contain the full multi-line text
	if len(m.inputHistory) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(m.inputHistory))
	}
	if m.inputHistory[0] != multiLine {
		t.Errorf("history[0] = %q, want %q", m.inputHistory[0], multiLine)
	}
	// Folded state should be cleared
	if m.inputFolded {
		t.Error("folded state should be cleared after submit")
	}
}

// TestTUI_InputFold_EscClears tests that Esc clears folded state.
func TestTUI_InputFold_EscClears(t *testing.T) {
	m := newTestModel()
	multiLine := "a\nb\nc\nd\ne"
	setInput(m, multiLine)

	// Fold
	m2, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = m2.(*tuiModel)
	if !m.inputFolded {
		t.Fatal("input should be folded")
	}

	// Simulate a running turn so Esc clears instead of interrupting
	m.turnRunning = false
	// Esc should clear folded state and reset input
	m2, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = m2.(*tuiModel)
	if m.inputFolded {
		t.Error("Esc should clear folded state")
	}
	if m.ta.Value() != "" {
		t.Errorf("textarea should be empty after Esc, got %q", m.ta.Value())
	}
}

// TestTUI_InputFold_CtrlQUsesFullText tests that Ctrl+Q uses full text when folded.
func TestTUI_InputFold_CtrlQUsesFullText(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true // Ctrl+Q queues when turn is running
	multiLine := "q1\nq2\nq3\nq4\nq5"
	setInput(m, multiLine)

	// Fold
	m2, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = m2.(*tuiModel)
	if !m.inputFolded {
		t.Fatal("input should be folded")
	}

	// Ctrl+Q should queue the full text
	m2, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m = m2.(*tuiModel)
	if len(m.queue) != 1 {
		t.Fatalf("expected 1 queued item, got %d", len(m.queue))
	}
	if m.queue[0].text != multiLine {
		t.Errorf("queued text = %q, want %q", m.queue[0].text, multiLine)
	}
	if m.inputFolded {
		t.Error("folded state should be cleared after Ctrl+Q")
	}
}

// TestTUI_InputFold_SingleLineNoFold tests that single-line input doesn't fold.
func TestTUI_InputFold_SingleLineNoFold(t *testing.T) {
	m := newTestModel()
	setInput(m, "single line")

	// Tab should not fold (only 1 line)
	m2, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = m2.(*tuiModel)
	if m.inputFolded {
		t.Error("single-line input should not fold")
	}
}

// TestTUI_InputFold_FourLinesNoFold tests that 4 lines don't trigger fold.
func TestTUI_InputFold_FourLinesNoFold(t *testing.T) {
	m := newTestModel()
	fourLines := "l1\nl2\nl3\nl4"
	setInput(m, fourLines)

	// Tab should not fold (only 4 lines, threshold is 5)
	m2, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = m2.(*tuiModel)
	if m.inputFolded {
		t.Error("4-line input should not fold (threshold is 5)")
	}
}

// TestTUI_InputFold_ShortTextTabFallsThroughToTextarea guards the non-fold
// path after the #1097 reorder: Tab on short (<5 line) input must still
// reach the textarea's own Update instead of being swallowed entirely by
// the fold-check, leaving the value unchanged and unfolded.
func TestTUI_InputFold_ShortTextTabFallsThroughToTextarea(t *testing.T) {
	m := newTestModel()
	setInput(m, "short")
	before := m.ta.Value()

	m2, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = m2.(*tuiModel)

	if m.inputFolded {
		t.Error("short input should not fold")
	}
	if m.ta.Value() != before {
		t.Errorf("textarea value = %q, want unchanged %q", m.ta.Value(), before)
	}
}

// pasteKey builds the bracketed-paste KeyMsg bubbletea delivers for a paste.
func pasteKey(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Paste: true}
}

// TestTUI_Paste_LargeFoldsToToken: a big multi-line paste is replaced by a
// "[#1 pasted N lines]" placeholder instead of dumping the raw text into the box.
func TestTUI_Paste_LargeFoldsToToken(t *testing.T) {
	m := newTestModel()
	big := "a\nb\nc\nd\ne\nf"
	m2, _ := m.handleKey(pasteKey(big))
	m = m2.(*tuiModel)

	if len(m.pastedBlocks) != 1 {
		t.Fatalf("expected 1 pasted block, got %d", len(m.pastedBlocks))
	}
	if got := m.ta.Value(); got != "[#1 pasted 6 lines]" {
		t.Errorf("box value = %q, want placeholder token", got)
	}
	if strings.Contains(m.ta.Value(), "\n") {
		t.Error("raw multi-line paste must not land in the box")
	}
}

// TestTUI_Paste_SmallInsertsVerbatim: a short paste below the threshold is
// inserted as-is so brief snippets stay visible and editable.
func TestTUI_Paste_SmallInsertsVerbatim(t *testing.T) {
	m := newTestModel()
	small := "one\ntwo"
	m2, _ := m.handleKey(pasteKey(small))
	m = m2.(*tuiModel)

	if len(m.pastedBlocks) != 0 {
		t.Errorf("small paste should not create a token, got %d blocks", len(m.pastedBlocks))
	}
	if m.ta.Value() != small {
		t.Errorf("box value = %q, want %q", m.ta.Value(), small)
	}
}

// TestTUI_Paste_LongSingleLineFoldsByChars: a single huge line (no newlines)
// folds via the character threshold and labels itself in chars.
func TestTUI_Paste_LongSingleLineFoldsByChars(t *testing.T) {
	m := newTestModel()
	long := strings.Repeat("x", pasteFoldMinChars)
	m2, _ := m.handleKey(pasteKey(long))
	m = m2.(*tuiModel)

	if len(m.pastedBlocks) != 1 {
		t.Fatalf("expected 1 pasted block, got %d", len(m.pastedBlocks))
	}
	want := fmt.Sprintf("[#1 pasted %d chars]", pasteFoldMinChars)
	if m.ta.Value() != want {
		t.Errorf("box value = %q, want %q", m.ta.Value(), want)
	}
}

// TestTUI_Paste_SubmitExpands: submit restores the full pasted text (model +
// history) while the scrollback echo keeps the collapsed token.
func TestTUI_Paste_SubmitExpands(t *testing.T) {
	m := newTestModel()
	big := "x1\nx2\nx3\nx4\nx5\nx6"
	m2, _ := m.handleKey(pasteKey(big))
	m = m2.(*tuiModel)
	// Type a prefix around the token.
	setInput(m, "review this: "+m.ta.Value())

	_, _ = m.submit()
	if !m.turnRunning {
		t.Fatal("submit should start a turn")
	}
	wantText := "review this: " + big
	if len(m.inputHistory) != 1 || m.inputHistory[0] != wantText {
		t.Errorf("history[0] = %q, want %q", m.inputHistory, wantText)
	}
	if len(m.pastedBlocks) != 0 {
		t.Error("pasted blocks should be cleared after submit")
	}
	// Scrollback echo stays collapsed (token, not raw paste).
	if !strings.Contains(m.echoPending, "[#1 pasted 6 lines]") {
		t.Errorf("echo should show the collapsed token, got %q", m.echoPending)
	}
	if strings.Contains(m.echoPending, "x6") {
		t.Errorf("echo must not dump the raw paste, got %q", m.echoPending)
	}
}

// TestTUI_Paste_CtrlQQueuesExpanded: queuing mid-turn stores the full text.
func TestTUI_Paste_CtrlQQueuesExpanded(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	big := "q1\nq2\nq3\nq4\nq5\nq6"
	m2, _ := m.handleKey(pasteKey(big))
	m = m2.(*tuiModel)

	m2, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m = m2.(*tuiModel)
	if len(m.queue) != 1 || m.queue[0].text != big {
		t.Errorf("queued text = %+v, want full paste", m.queue)
	}
	if len(m.pastedBlocks) != 0 {
		t.Error("pasted blocks should be cleared after Ctrl+Q")
	}
}

// TestTUI_Paste_EscClears: Esc on an idle box discards pending pastes.
func TestTUI_Paste_EscClears(t *testing.T) {
	m := newTestModel()
	m.turnRunning = false
	big := "e1\ne2\ne3\ne4\ne5\ne6"
	m2, _ := m.handleKey(pasteKey(big))
	m = m2.(*tuiModel)
	if len(m.pastedBlocks) != 1 {
		t.Fatalf("expected a pending paste, got %d", len(m.pastedBlocks))
	}

	m2, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = m2.(*tuiModel)
	if len(m.pastedBlocks) != 0 {
		t.Error("Esc should clear pending pastes")
	}
	if m.ta.Value() != "" {
		t.Errorf("box should be empty after Esc, got %q", m.ta.Value())
	}
}

// show_artifact's whole point is presenting a file to the user; in the rich
// TUI its status line carries an OSC 8 file:// hyperlink so the artifact is
// click-to-open. Errors and --plain keep the generic status forms (no escapes
// in the plain/headless transcript).
func TestTUI_RenderToolOutcome_ArtifactHyperlink(t *testing.T) {
	m := newTestModel()
	// Platform-appropriate absolute path: linkify is gated on filepath.IsAbs,
	// and a rootless /tmp/... is NOT absolute on Windows.
	path := "/tmp/report.html"
	if runtime.GOOS == "windows" {
		path = `C:\tmp\report.html`
	}
	input := map[string]any{"path": path}

	got := m.renderToolOutcome("show_artifact", input, "Artifact presented: "+path+" (12 bytes)", false, 0)
	if !strings.Contains(got, "\x1b]8;;"+tui.FileURI(path)+"\x1b\\") {
		t.Errorf("success line should carry an OSC 8 file:// hyperlink; got %q", got)
	}
	if !strings.Contains(got, path) {
		t.Errorf("success line should show the path; got %q", got)
	}

	// A relative path must NOT be linkified — re-resolving it at render time
	// diverges from the execute-time cwd on resumed-history replay.
	if got := m.renderToolOutcome("show_artifact", map[string]any{"path": "rel/report.html"}, "ok", false, 0); strings.Contains(got, "\x1b]8;;") {
		t.Errorf("relative path should not render a hyperlink; got %q", got)
	}

	if got := m.renderToolOutcome("show_artifact", input, "boom", true, 0); strings.Contains(got, "\x1b]8;;") {
		t.Errorf("error outcome should not render a hyperlink; got %q", got)
	}

	m.cfg.plain = true
	if got := m.renderToolOutcome("show_artifact", input, "ok", false, 0); strings.Contains(got, "\x1b]8;;") {
		t.Errorf("--plain outcome should not render a hyperlink; got %q", got)
	}
}
