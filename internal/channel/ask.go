package channel

import "fmt"

// The interactive permission ask for IM channels: a tool call that resolves
// to an ask-class verdict sends a confirmation prompt into the chat, and the
// session's NEXT plain message is consumed as the answer. The inbound
// dispatcher routes that message via DeliverAskReply BEFORE spawning a turn —
// routing it through the normal turn path would deadlock behind runMu, which
// the asking turn still holds.

// BeginAsk claims the session's single ask slot and returns the channel on
// which the answer text will arrive, plus a release func the caller must run
// when the ask resolves (answer, timeout, or cancellation). chatID and userID
// pin who may answer: only a reply from the same chat by the same user is
// accepted — session keying usually guarantees this already (BindByChatUser),
// but the slot enforces it so the property survives other binding modes,
// where a session is shared across users or chats. A second BeginAsk while
// one is pending is refused — within a session turns are serialised, so this
// only guards against misuse.
func (s *Session) BeginAsk(chatID, userID string) (<-chan string, func(), error) {
	s.askMu.Lock()
	defer s.askMu.Unlock()
	if s.pendingAsk != nil {
		return nil, nil, fmt.Errorf("an approval prompt is already pending in this chat")
	}
	ch := make(chan string, 1)
	s.pendingAsk = ch
	s.askChatID, s.askUserID = chatID, userID
	s.askButtonsOnly = false
	release := func() {
		s.askMu.Lock()
		if s.pendingAsk == ch {
			s.pendingAsk = nil
			s.askButtonsOnly = false
		}
		s.askMu.Unlock()
	}
	return ch, release, nil
}

// SetAskButtonsOnly marks the pending ask as button-only: DeliverAskReply will
// NOT consume plain text messages — only DeliverAskButton can resolve. Must
// be called after BeginAsk and before any reply arrives. Has no effect when no
// ask is pending.
func (s *Session) SetAskButtonsOnly() {
	s.askMu.Lock()
	s.askButtonsOnly = true
	s.askMu.Unlock()
}

// DeliverAskReply routes text to a pending ask and reports whether it was
// consumed. False means no ask is waiting (or the reply came from the wrong
// chat or user) — the caller should treat the message as normal chat input.
// When askButtonsOnly is true (button-based ask), returns false so plain text
// messages stay as ordinary chat input instead of being swallowed (#1120).
// Each ask consumes exactly one reply.
func (s *Session) DeliverAskReply(chatID, userID, text string) bool {
	s.askMu.Lock()
	defer s.askMu.Unlock()
	if s.pendingAsk == nil || s.askButtonsOnly {
		return false
	}
	if chatID != s.askChatID || userID != s.askUserID {
		return false
	}
	s.pendingAsk <- text // buffered (cap 1); never blocks
	s.pendingAsk = nil
	s.askButtonsOnly = false
	return true
}

// DeliverAskButton routes a button press callback to a pending ask and reports
// whether it was consumed. Works regardless of askButtonsOnly mode — this is
// the intended resolution path for button-based asks. Each ask consumes
// exactly one button press.
func (s *Session) DeliverAskButton(chatID, userID, buttonID string) bool {
	s.askMu.Lock()
	defer s.askMu.Unlock()
	if s.pendingAsk == nil {
		return false
	}
	if chatID != s.askChatID || userID != s.askUserID {
		return false
	}
	s.pendingAsk <- buttonID // buffered (cap 1); never blocks
	s.pendingAsk = nil
	s.askButtonsOnly = false
	return true
}
