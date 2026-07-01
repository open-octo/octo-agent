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
