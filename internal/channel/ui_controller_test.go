package channel

import (
	"context"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

func TestUIController_TextDeltaAccumulation(t *testing.T) {
	mock := &mockAdapter{platform: "mock"}
	ctrl := NewUIController(mock, "chat1", "msg1")
	handler := ctrl.Handler()

	// Simulate streaming text deltas.
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "Hello "})
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "world"})

	// Before turn_done, text should be buffered (not yet sent for small buffers).
	if mock.sentTextCount() != 0 {
		t.Fatalf("expected 0 sent texts before turn_done, got %d", mock.sentTextCount())
	}

	// End the turn.
	handler(agent.AgentEvent{Kind: agent.EventTurnDone, Reply: &agent.Reply{Content: "Hello world"}})

	if mock.sentTextCount() != 1 {
		t.Fatalf("expected 1 sent text after turn_done, got %d", mock.sentTextCount())
	}
	last := mock.lastSentText()
	if last.text != "Hello world" {
		t.Fatalf("unexpected text: %q", last.text)
	}
	if last.chatID != "chat1" {
		t.Fatalf("unexpected chatID: %q", last.chatID)
	}
}

func TestUIController_ToolEventsSuppressed(t *testing.T) {
	mock := &mockAdapter{platform: "mock"}
	ctrl := NewUIController(mock, "chat1", "")
	handler := ctrl.Handler()

	// Tool started — should not send anything.
	handler(agent.AgentEvent{Kind: agent.EventToolStarted, ToolName: "read_file"})
	if mock.sentTextCount() != 0 {
		t.Fatalf("expected no message on tool start, got %d", mock.sentTextCount())
	}

	// Tool progress — should be suppressed.
	handler(agent.AgentEvent{Kind: agent.EventToolProgress, ToolName: "read_file", Chunk: "line1\n"})
	if mock.sentTextCount() != 0 {
		t.Fatalf("expected no message on tool progress, got %d", mock.sentTextCount())
	}

	// Tool input delta — should be suppressed.
	handler(agent.AgentEvent{Kind: agent.EventToolInputDelta, ToolName: "read_file", InputDelta: "{"})
	if mock.sentTextCount() != 0 {
		t.Fatalf("expected no message on tool input delta, got %d", mock.sentTextCount())
	}

}

func TestUIController_ToolDoneSuppressed(t *testing.T) {
	mock := &mockAdapter{platform: "mock"}
	ctrl := NewUIController(mock, "chat1", "")
	handler := ctrl.Handler()

	output := "Read file: /path/to/file.go\nContent: package main"
	handler(agent.AgentEvent{Kind: agent.EventToolStarted, ToolName: "read_file"})
	handler(agent.AgentEvent{Kind: agent.EventToolDone, ToolName: "read_file", Output: output})

	if mock.sentTextCount() != 0 {
		t.Fatalf("expected no message for tool done, got %d", mock.sentTextCount())
	}

	// End turn should only flush the model's reply text, not a file summary.
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "Done"})
	handler(agent.AgentEvent{Kind: agent.EventTurnDone, Reply: &agent.Reply{Content: "Done"}})

	if mock.sentTextCount() != 1 {
		t.Fatalf("expected 1 reply text, got %d", mock.sentTextCount())
	}
	if strings.Contains(mock.lastSentText().text, "file.go") {
		t.Fatalf("expected no file summary, got: %q", mock.lastSentText().text)
	}
}

func TestUIController_ToolErrorSuppressed(t *testing.T) {
	mock := &mockAdapter{platform: "mock"}
	ctrl := NewUIController(mock, "chat1", "")
	handler := ctrl.Handler()

	handler(agent.AgentEvent{Kind: agent.EventToolStarted, ToolName: "read_file"})
	handler(agent.AgentEvent{Kind: agent.EventToolError, ToolName: "read_file", Err: "permission denied", Output: ""})

	if mock.sentTextCount() != 0 {
		t.Fatalf("expected no chat message for tool error, got %d", mock.sentTextCount())
	}
}

func TestUIController_ShouldFlush(t *testing.T) {
	// Paragraph break triggers flush.
	if !shouldFlush("Hello\n\n") {
		t.Error("expected flush on paragraph break")
	}
	// Sentence end triggers flush.
	if !shouldFlush("Hello. ") {
		t.Error("expected flush on sentence end")
	}
	// Long buffer triggers flush.
	long := strings.Repeat("a", 900)
	if !shouldFlush(long) {
		t.Error("expected flush on long buffer")
	}
	// Short incomplete sentence does not flush.
	if shouldFlush("Hel") {
		t.Error("expected no flush on short text")
	}
	// #1116: the ASCII-only sentence check never flushed CJK prose, which
	// doesn't put a space after 。！？ the way English does after . ! ? —
	// only the 800-byte cap or \n\n ever flushed Chinese/Japanese text.
	if !shouldFlush("你好，世界。") {
		t.Error("expected flush on CJK period 。")
	}
	if !shouldFlush("太好了！") {
		t.Error("expected flush on CJK exclamation ！")
	}
	if !shouldFlush("真的吗？") {
		t.Error("expected flush on CJK question mark ？")
	}
	if shouldFlush("还没说完") {
		t.Error("expected no flush on CJK text with no sentence-ending punctuation")
	}
}

func TestUIController_Truncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("expected no truncation")
	}
	if truncate("hello world", 5) != "hello…" {
		t.Errorf("unexpected truncation: %q", truncate("hello world", 5))
	}
}

func TestUIController_MessageUpdate(t *testing.T) {
	mock := &mockAdapter{platform: "mock"}
	ctrl := NewUIController(mock, "chat1", "")
	handler := ctrl.Handler()

	// First delta — should send a new message.
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "Hello"})
	handler(agent.AgentEvent{Kind: agent.EventTurnDone, Reply: &agent.Reply{Content: "Hello"}})

	if mock.sentTextCount() != 1 {
		t.Fatalf("expected 1 sent text, got %d", mock.sentTextCount())
	}

	// Reset and simulate a platform that supports updates.
	mock = &mockAdapter{platform: "mock"}
	ctrl = NewUIController(mock, "chat1", "")
	handler = ctrl.Handler()

	// Simulate a large text that gets flushed mid-stream, then updated.
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: strings.Repeat("a", 900)})
	// The large buffer triggers a flush.
	if mock.sentTextCount() != 1 {
		t.Fatalf("expected 1 sent text after large buffer flush, got %d", mock.sentTextCount())
	}

	// Turn done should flush any remaining text.
	handler(agent.AgentEvent{Kind: agent.EventTurnDone, Reply: &agent.Reply{Content: strings.Repeat("a", 900)}})
	// May send another message or update — either is acceptable.
}

func TestRunAgent(t *testing.T) {
	mock := &mockAdapter{platform: "mock"}
	ctrl := NewUIController(mock, "chat1", "")

	sess := &Session{
		Key:    "test",
		Agent:  agent.New(fakeSender{}, "test-model"),
		ChatID: "chat1",
	}

	ctx := context.Background()
	reply, err := RunAgent(ctx, sess, nil, nil, ctrl, "hello")
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}
	if reply.Content != "ok" {
		t.Fatalf("unexpected reply: %q", reply.Content)
	}

	// Should have sent the reply text via the controller.
	if mock.sentTextCount() != 1 {
		t.Fatalf("expected 1 sent text, got %d", mock.sentTextCount())
	}
}

// A multi-flush reply on an update-capable platform (Telegram/Discord/Feishu)
// must edit the message with the FULL text streamed so far — an edit carrying
// only the newest chunk would erase what the user already read (#1115).
func TestUIController_UpdateCarriesFullText(t *testing.T) {
	mock := &mockAdapter{platform: "mock", issueMsgIDs: true}
	ctrl := NewUIController(mock, "chat1", "")
	handler := ctrl.Handler()

	// First flush: paragraph break → new message with chunk 1.
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "Paragraph one.\n\n"})
	if mock.sentTextCount() != 1 {
		t.Fatalf("expected 1 sent text after first flush, got %d", mock.sentTextCount())
	}
	if got := mock.lastSentText().text; got != "Paragraph one." {
		t.Fatalf("first message = %q", got)
	}

	// Second flush: must EDIT message m1 with paragraphs one AND two.
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "Paragraph two.\n\n"})
	mock.mu.Lock()
	updates := append([]updatedMsg(nil), mock.updatedMsgs...)
	mock.mu.Unlock()
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if updates[0].messageID != "m1" {
		t.Fatalf("update should target the first message, got %q", updates[0].messageID)
	}
	if want := "Paragraph one.\n\nParagraph two."; updates[0].text != want {
		t.Fatalf("update must carry the full text:\nwant %q\ngot  %q", want, updates[0].text)
	}
	if mock.sentTextCount() != 1 {
		t.Fatalf("no second message expected while edits succeed, got %d", mock.sentTextCount())
	}

	// Turn end flushes the tail into the same message.
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "Tail."})
	handler(agent.AgentEvent{Kind: agent.EventTurnDone, Reply: &agent.Reply{}})
	mock.mu.Lock()
	final := mock.updatedMsgs[len(mock.updatedMsgs)-1]
	mock.mu.Unlock()
	if want := "Paragraph one.\n\nParagraph two.\n\nTail."; final.text != want {
		t.Fatalf("final edit must carry everything:\nwant %q\ngot  %q", want, final.text)
	}
}

// When an edit fails (platform edit-size cap, deleted message), the reply
// continues in a fresh message carrying only the not-yet-shown chunk, and
// subsequent edits target the new message with its own accumulated text.
func TestUIController_UpdateFailureStartsFreshMessage(t *testing.T) {
	mock := &mockAdapter{platform: "mock", issueMsgIDs: true, failUpdates: true}
	ctrl := NewUIController(mock, "chat1", "")
	handler := ctrl.Handler()

	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "One.\n\n"})
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "Two.\n\n"})

	if mock.sentTextCount() != 2 {
		t.Fatalf("expected 2 messages when edits fail, got %d", mock.sentTextCount())
	}
	if got := mock.lastSentText().text; got != "Two." {
		t.Fatalf("second message should carry only the new chunk, got %q", got)
	}

	// Edits recover: the next flush must edit the SECOND message with its
	// accumulated text only (not the first message's content).
	mock.mu.Lock()
	mock.failUpdates = false
	mock.mu.Unlock()
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "Three.\n\n"})
	mock.mu.Lock()
	updates := append([]updatedMsg(nil), mock.updatedMsgs...)
	mock.mu.Unlock()
	if len(updates) != 1 {
		t.Fatalf("expected 1 update after recovery, got %d", len(updates))
	}
	if updates[0].messageID != "m2" {
		t.Fatalf("update should target the second message, got %q", updates[0].messageID)
	}
	if want := "Two.\n\nThree."; updates[0].text != want {
		t.Fatalf("recovered edit text:\nwant %q\ngot  %q", want, updates[0].text)
	}
}

// Platforms without message updates keep the existing behavior: each flush is
// its own message.
func TestUIController_NoUpdatesPlatformSendsChunks(t *testing.T) {
	mock := &mockAdapter{platform: "mock", issueMsgIDs: true, noUpdates: true}
	ctrl := NewUIController(mock, "chat1", "")
	handler := ctrl.Handler()

	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "One.\n\n"})
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "Two.\n\n"})
	handler(agent.AgentEvent{Kind: agent.EventTurnDone, Reply: &agent.Reply{}})

	if mock.sentTextCount() != 2 {
		t.Fatalf("expected 2 chunk messages, got %d", mock.sentTextCount())
	}
	mock.mu.Lock()
	nUpdates := len(mock.updatedMsgs)
	first := mock.sentTexts[0].text
	second := mock.sentTexts[1].text
	mock.mu.Unlock()
	if nUpdates != 0 {
		t.Fatalf("no edits expected, got %d", nUpdates)
	}
	if first != "One." || second != "Two." {
		t.Fatalf("chunks = %q, %q", first, second)
	}
}

// A new turn must not edit the previous turn's message.
func TestUIController_NewTurnStartsNewMessage(t *testing.T) {
	mock := &mockAdapter{platform: "mock", issueMsgIDs: true}
	ctrl := NewUIController(mock, "chat1", "")
	handler := ctrl.Handler()

	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "Turn one."})
	handler(agent.AgentEvent{Kind: agent.EventTurnDone, Reply: &agent.Reply{}})
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "Turn two."})
	handler(agent.AgentEvent{Kind: agent.EventTurnDone, Reply: &agent.Reply{}})

	if mock.sentTextCount() != 2 {
		t.Fatalf("expected 2 separate messages, got %d", mock.sentTextCount())
	}
	mock.mu.Lock()
	nUpdates := len(mock.updatedMsgs)
	mock.mu.Unlock()
	if nUpdates != 0 {
		t.Fatalf("a new turn must not edit the old turn's message, got %d edits", nUpdates)
	}
}

// When both the edit and the fallback send fail on the same flush, the chunk
// must not be dropped forever — it is re-queued and retried (carried along
// with whatever text follows) on the next successful flush.
func TestUIController_DoubleFailureRetriesChunk(t *testing.T) {
	mock := &mockAdapter{platform: "mock", issueMsgIDs: true}
	ctrl := NewUIController(mock, "chat1", "")
	handler := ctrl.Handler()

	// Establish a pending message normally.
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "One.\n\n"})
	if mock.sentTextCount() != 1 {
		t.Fatalf("expected 1 sent text, got %d", mock.sentTextCount())
	}

	// Both the edit and the fallback send fail for the next chunk.
	mock.mu.Lock()
	mock.failUpdates = true
	mock.failSends = true
	mock.mu.Unlock()
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "Two.\n\n"})

	if mock.sentTextCount() != 1 {
		t.Fatalf("no new message should be delivered on double failure, got %d", mock.sentTextCount())
	}
	mock.mu.Lock()
	nUpdates := len(mock.updatedMsgs)
	mock.mu.Unlock()
	if nUpdates != 0 {
		t.Fatalf("no update should be recorded on double failure, got %d", nUpdates)
	}

	// Recovery: the next successful flush must carry the RETRIED "Two." along
	// with "Three." — losing "Two." here would mean silent content loss.
	mock.mu.Lock()
	mock.failUpdates = false
	mock.failSends = false
	mock.mu.Unlock()
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "Three.\n\n"})
	mock.mu.Lock()
	updates := append([]updatedMsg(nil), mock.updatedMsgs...)
	mock.mu.Unlock()
	if len(updates) != 1 {
		t.Fatalf("expected 1 update after recovery, got %d", len(updates))
	}
	if want := "One.\n\nTwo.\n\nThree."; updates[0].text != want {
		t.Fatalf("retried chunk must survive:\nwant %q\ngot  %q", want, updates[0].text)
	}
}

// #1116: on a platform without in-place edits, every flush is its own
// standalone IM message. A code fence spanning two flushes must close at
// the end of the first message and reopen (same language tag) at the start
// of the second — otherwise the first message renders a permanently
// unclosed code block and the second resumes mid-code with no fence at all.
func TestUIController_NoUpdatesPlatformCarriesFenceAcrossFlushes(t *testing.T) {
	mock := &mockAdapter{platform: "mock", issueMsgIDs: true, noUpdates: true}
	ctrl := NewUIController(mock, "chat1", "")
	handler := ctrl.Handler()

	// Flush 1 (paragraph-break heuristic fires mid-fence): the buffer has an
	// opened but not yet closed ```go block.
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "```go\nline1\n\n"})
	// Flush 2 (turn end): the fence closes here, with more text after it.
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "line2\n```\n\nDone"})
	handler(agent.AgentEvent{Kind: agent.EventTurnDone, Reply: &agent.Reply{}})

	mock.mu.Lock()
	texts := make([]string, len(mock.sentTexts))
	for i, s := range mock.sentTexts {
		texts[i] = s.text
	}
	mock.mu.Unlock()
	if len(texts) != 2 {
		t.Fatalf("expected 2 messages, got %d: %v", len(texts), texts)
	}
	if want := "```go\nline1\n```"; texts[0] != want {
		t.Errorf("message 1:\nwant %q\ngot  %q", want, texts[0])
	}
	if want := "```go\nline2\n```\n\nDone"; texts[1] != want {
		t.Errorf("message 2:\nwant %q\ngot  %q", want, texts[1])
	}
	for i, text := range texts {
		if open, _ := fenceStateAfter(text, false, ""); open {
			t.Errorf("message %d ends with an unclosed fence: %q", i, text)
		}
	}
}

// #1116: when an in-place edit fails partway through a code block, the
// fallback to a fresh SendText message must reopen the fence based on the
// FROZEN message's actual content (u.sentText) — not on some earlier
// SendText call's fence state, which predates everything the user has seen
// via edits since and would be stale.
func TestUIController_EditFailureRecomputesFenceFromFrozenText(t *testing.T) {
	mock := &mockAdapter{platform: "mock", issueMsgIDs: true}
	ctrl := NewUIController(mock, "chat1", "")
	handler := ctrl.Handler()

	// Flush 1 (SendText — no pending message yet): plain text, no fence.
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "Intro.\n\n"})
	// Flush 2 (successful edit): opens a fence inside the edited message.
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "```go\nfunc foo() {\n\n"})

	mock.mu.Lock()
	if len(mock.sentTexts) != 1 {
		t.Fatalf("expected exactly 1 SendText (the first message), got %d", len(mock.sentTexts))
	}
	if len(mock.updatedMsgs) != 1 {
		t.Fatalf("expected exactly 1 successful edit, got %d", len(mock.updatedMsgs))
	}
	// Now the edit-size cap kicks in for the next flush.
	mock.failUpdates = true
	mock.mu.Unlock()

	// Flush 3 (turn end, edit fails): closes the fence and adds trailing text.
	handler(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "more code\n```\n\nDone"})
	handler(agent.AgentEvent{Kind: agent.EventTurnDone, Reply: &agent.Reply{}})

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.sentTexts) != 2 {
		t.Fatalf("expected a second standalone message after the edit failed, got %d sends", len(mock.sentTexts))
	}
	fallback := mock.sentTexts[1].text
	if want := "```go\nmore code\n```\n\nDone"; fallback != want {
		t.Errorf("fallback message:\nwant %q\ngot  %q", want, fallback)
	}
	if open, _ := fenceStateAfter(fallback, false, ""); open {
		t.Errorf("fallback message ends with an unclosed fence: %q", fallback)
	}
}
