package channel

import (
	"context"
	"strings"
	"sync"

	"github.com/open-octo/octo-agent/internal/agent"
)

// UIController bridges agent.AgentEvent stream to IM platform messages.
// It suppresses noisy tool-call events and forwards only the model's text
// reply to the adapter.
type UIController struct {
	adapter Adapter
	chatID  string
	replyTo string

	// mu guards the builder state below.
	mu sync.Mutex

	// textBuf accumulates text deltas until a turn completes or a tool event
	// forces a flush. This batches rapid deltas into fewer IM messages.
	textBuf strings.Builder

	// pendingTextMsgID is the message ID of the last text message we sent,
	// used for in-place updates on platforms that support it.
	pendingTextMsgID string

	// sentText is the raw stream text already delivered into the message
	// identified by pendingTextMsgID. Platform edits REPLACE the whole
	// message (Telegram editMessageText; Discord and Feishu likewise), so an
	// in-place update must carry the full accumulated text — editing with
	// only the newest chunk would erase what the user already read.
	sentText string

	// fenceOpen/fenceLang track a ``` code fence left open by the last text
	// actually handed to adapter.SendText (#1116). Every SendText call
	// produces its own standalone IM message — unlike UpdateMessage, which
	// redraws the full cumulative text each time and so "sees" the true,
	// complete fence state on its own. A fence opened in one flush and
	// closed in a later one would otherwise render as a permanently broken
	// code block on any platform without in-place edits (DingTalk/WeCom/
	// Weixin: every flush is a SendText call), or after an edit-size-cap
	// fallback freezes an edit-capable platform's message mid-fence.
	fenceOpen bool
	fenceLang string

	// toolCount tracks how many tools have started (for suppression heuristics).
	toolCount int

	// inTool is true while we're between EventToolStarted and its matching done/error.
	inTool bool

	// stopTyping cancels the caller's typing-keepalive ticker (see
	// startTypingKeepalive in internal/server). Invoked once, the first time
	// this turn's reply text is actually flushed to the adapter — the reply
	// itself is then evidence the bot is alive, so there's no need to keep
	// re-asserting typing for the rest of the turn or any turns chained after
	// it (#1117). nil for callers that don't drive a keepalive (e.g. idle
	// follow-up turns).
	stopTyping func()

	// typingStopped guards stopTyping so it fires at most once. Deliberately
	// not reset by resetLocked: this controller is reused across chained
	// turns within one inbound message, and once the user has seen a reply
	// there's no need to re-arm typing for later turns in the same chain.
	typingStopped bool

	// suppressor, when non-nil, is checked before every outbound message. If it
	// returns true, the message is silently dropped instead of delivered to the
	// chat — used by /unbind to detach the chat mid-turn: the turn keeps running
	// and its history still persists, but nothing more is sent to this IM chat.
	// The func is polled (not snapshotted) so a /unbind that lands after the
	// controller was built still takes effect for the rest of the turn.
	suppressor func() bool
}

// NewUIController creates a controller bound to one chat conversation.
// stopTyping, if non-nil, is invoked once on the first text this turn (or
// any turn chained after it) flushes to the adapter.
func NewUIController(adapter Adapter, chatID, replyTo string, stopTyping func()) *UIController {
	return &UIController{
		adapter:    adapter,
		chatID:     chatID,
		replyTo:    replyTo,
		stopTyping: stopTyping,
	}
}

// SetSuppressor sets the suppressor func consulted before each outbound
// message. A nil func clears suppression. See the suppressor field.
func (u *UIController) SetSuppressor(fn func() bool) {
	u.suppressor = fn
}

// SuppressDelivery reports whether this session's outbound IM delivery is
// currently suppressed (set by /unbind while a turn is in flight). The
// runChannelTurns handler wires this into its UIController so a mid-turn
// /unbind drops the rest of the reply.
func (s *Session) SuppressDelivery() bool {
	return s.suppressDelivery.Load()
}

// suppressed reports whether outbound delivery is currently suppressed,
// polling the suppressor func if one is set.
func (u *UIController) suppressed() bool {
	return u.suppressor != nil && u.suppressor()
}

// Handler returns an agent.EventHandler that forwards events to the adapter.
func (u *UIController) Handler() agent.EventHandler {
	return func(ev agent.AgentEvent) {
		u.handleEvent(ev)
	}
}

// handleEvent routes each AgentEvent to the appropriate handler.
func (u *UIController) handleEvent(ev agent.AgentEvent) {
	switch ev.Kind {
	case agent.EventTextDelta:
		u.onTextDelta(ev.Text)
	case agent.EventToolStarted:
		u.onToolStarted(ev.ToolName)
	case agent.EventToolProgress:
		// Suppress progress noise in IM — too chatty.
	case agent.EventToolDone:
		u.onToolDone(ev.ToolName)
	case agent.EventToolError:
		// Suppress tool errors in IM — the model sees the error result in
		// context and can explain it in its own reply; a separate ❌ message
		// is noisy and can leak implementation details into group chats.
		u.onToolError(ev.ToolName)
	case agent.EventTurnDone:
		u.onTurnDone(ev.Reply)
	case agent.EventToolInputDelta:
		// Tool input deltas are UI-only (web TUI) — suppress in IM.
	}
}

// onTextDelta accumulates text and periodically flushes to the adapter.
func (u *UIController) onTextDelta(text string) {
	if text == "" {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()

	u.textBuf.WriteString(text)

	// Flush when we hit a natural boundary or the buffer grows large.
	buf := u.textBuf.String()
	if shouldFlush(buf) {
		u.flushTextLocked()
	}
}

// onToolStarted notes that a tool has started. We may send a brief "working on X"
// indicator on platforms that support updates, but generally suppress tool-start
// noise in IM.
func (u *UIController) onToolStarted(toolName string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.toolCount++
	u.inTool = true
}

// onToolDone notes that a tool completed successfully. Tool output is not
// surfaced as a separate chat message; only the model's text reply is shown.
func (u *UIController) onToolDone(toolName string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.inTool = false
}

// onToolError notes that a tool failed. The error is not surfaced as a chat
// message; the model receives the error in the tool result and can address it
// in its reply.
func (u *UIController) onToolError(toolName string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.inTool = false
}

// onTurnDone finalizes the turn: flushes remaining text, resets state, then
// force-drains any adapter send queue so the final message is delivered
// immediately instead of waiting for the background flush timer. When
// suppressed (chat detached mid-turn), the buffered text is dropped in
// flushTextLocked and there is nothing to drain — skip Flush so we don't
// push anything more to a chat the user already left.
func (u *UIController) onTurnDone(reply *agent.Reply) {
	u.mu.Lock()
	u.flushTextLocked()
	u.resetLocked()
	u.mu.Unlock() // explicit unlock before Flush — Flush may block during send

	if u.suppressed() {
		return
	}
	u.adapter.Flush(u.chatID)
}

// flushTextLocked delivers the accumulated text buffer to the adapter.
// Must be called with mu held.
func (u *UIController) flushTextLocked() {
	chunk := u.textBuf.String()
	if strings.TrimSpace(chunk) == "" {
		return
	}
	u.textBuf.Reset()

	// /unbind mid-turn: the chat is being detached, so stop the typing
	// indicator and silently drop the buffered reply instead of sending it.
	// The turn keeps running and its history still persists; nothing more is
	// delivered to this chat.
	if u.suppressed() {
		if !u.typingStopped {
			u.typingStopped = true
			if u.stopTyping != nil {
				u.stopTyping()
			}
		}
		return
	}

	if !u.typingStopped {
		u.typingStopped = true
		if u.stopTyping != nil {
			u.stopTyping()
		}
	}

	// If the platform supports message updates and we already have a pending
	// message, edit it in place with the FULL text streamed so far — the raw
	// chunks are concatenated unmodified so paragraph breaks survive, and
	// only the outer whitespace is trimmed for display.
	if u.adapter.SupportsMessageUpdates() && u.pendingTextMsgID != "" {
		full := u.sentText + chunk
		if u.adapter.UpdateMessage(u.chatID, u.pendingTextMsgID, strings.TrimSpace(full)) {
			u.sentText = full
			return
		}
		// Edit failed (platform edit-size cap, message deleted): fall through
		// and continue in a fresh message carrying just the not-yet-shown
		// chunk. The old message keeps its last successfully edited content —
		// u.sentText, pre-delta — and that's now frozen, so recompute the
		// fence state from that actual displayed text (#1116). u.fenceOpen
		// only ever tracked SendText calls, which for an edit-capable
		// platform means it's stale by everything shown via edits since.
		u.fenceOpen, u.fenceLang = fenceStateAfter(u.sentText, false, "")
	}

	// #1116: this chunk becomes its own standalone message. If a fence was
	// left open by whatever was last actually sent — the previous flush's
	// SendText, or the frozen content of a message an edit-cap fallback just
	// abandoned above — reopen it here, and close it again if this chunk
	// itself ends mid-fence, so a code block spanning two messages still
	// renders as valid markdown in both.
	trimmed := strings.TrimSpace(chunk)
	sendText := reopenFence(u.fenceOpen, u.fenceLang, trimmed)
	open, lang := fenceStateAfter(trimmed, u.fenceOpen, u.fenceLang)
	if open {
		sendText = strings.TrimRight(sendText, "\n") + "\n```"
	}

	res := u.adapter.SendText(u.chatID, sendText, u.replyTo)
	if res.OK {
		u.pendingTextMsgID = res.MessageID
		u.sentText = chunk
		u.fenceOpen, u.fenceLang = open, lang
		return
	}
	// Both the edit (if attempted) and the fresh send failed. Put the chunk
	// back into the buffer instead of dropping it forever: pendingTextMsgID,
	// sentText, and the fence state are left untouched (still tracking the
	// old message's true last-shown content), and the next flush — a later
	// delta, or the turn-end flush — retries this text along with whatever
	// comes after it.
	u.textBuf.WriteString(chunk)
}

// resetLocked clears per-turn state. Must be called with mu held.
func (u *UIController) resetLocked() {
	u.textBuf.Reset()
	u.pendingTextMsgID = ""
	u.sentText = ""
	u.fenceOpen = false
	u.fenceLang = ""
	u.toolCount = 0
	u.inTool = false
}

// shouldFlush returns true when the buffer should be sent.
// It triggers on paragraph boundaries or when the buffer exceeds a threshold.
func shouldFlush(buf string) bool {
	const maxBuf = 800
	if len(buf) >= maxBuf {
		return true
	}
	// Flush on paragraph break (double newline).
	if strings.HasSuffix(buf, "\n\n") {
		return true
	}
	// Flush on sentence-ending punctuation followed by space or newline.
	if n := len(buf); n > 2 {
		c := buf[n-2]
		if (c == '.' || c == '!' || c == '?') && (buf[n-1] == ' ' || buf[n-1] == '\n') {
			return true
		}
	}
	// #1116: the check above is ASCII-only, so CJK prose never flushed on a
	// sentence boundary — only on \n\n or the 800-byte cap. Chinese/Japanese
	// sentence-ending punctuation (。！？) isn't followed by a space the way
	// English is; the punctuation itself is the boundary.
	for _, p := range [...]string{"。", "！", "？"} {
		if strings.HasSuffix(buf, p) {
			return true
		}
	}
	return false
}

// truncate limits a string to at most maxLen runes, adding an ellipsis if
// truncated. Uses rune-aware slicing so multi-byte CJK characters are never
// split (byte slicing a CJK string mid-rune produces "�" replacement chars).
func truncate(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen]) + "…"
}

// RunAgent executes the agent run loop for a session and bridges events to the
// IM adapter. It also persists the turn's progress incrementally: after any
// event that grows or rewrites history, history is flushed to disk right
// away, so a process crash mid-turn loses at most the round in flight, not
// the whole turn — parity with the web WS handler's persistTurnProgress. The
// length/dirty gate keeps the per-event calls free when nothing changed.
func RunAgent(ctx context.Context, sess *Session, tools []agent.ToolDefinition, executor agent.ToolExecutor, ctrl *UIController, userInput string) (agent.Reply, error) {
	a := sess.Agent
	forward := ctrl.Handler()
	lastSavedLen := -1
	handler := func(ev agent.AgentEvent) {
		forward(ev)
		if n := a.History.Len(); n != lastSavedLen || a.History.RewriteDirty() {
			if sess.Persist() == nil {
				lastSavedLen = n
			}
		}
	}
	return a.RunStream(ctx, userInput, tools, executor, handler)
}
