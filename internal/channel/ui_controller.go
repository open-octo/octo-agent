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

	// sentTyping tracks whether we've already sent a typing indicator this turn.
	sentTyping bool
}

// NewUIController creates a controller bound to one chat conversation.
func NewUIController(adapter Adapter, chatID, replyTo string) *UIController {
	return &UIController{
		adapter: adapter,
		chatID:  chatID,
		replyTo: replyTo,
	}
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
// immediately instead of waiting for the background flush timer.
func (u *UIController) onTurnDone(reply *agent.Reply) {
	u.mu.Lock()
	u.flushTextLocked()
	u.resetLocked()
	u.mu.Unlock() // explicit unlock before Flush — Flush may block during send

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
	u.sentTyping = false
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

// truncate limits a string to maxLen, adding an ellipsis if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// RunAgent executes the agent run loop for a session and bridges events to the IM adapter.
func RunAgent(ctx context.Context, sess *Session, tools []agent.ToolDefinition, executor agent.ToolExecutor, ctrl *UIController, userInput string) (agent.Reply, error) {
	return sess.Agent.RunStream(ctx, userInput, tools, executor, ctrl.Handler())
}
