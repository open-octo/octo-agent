package agent

import (
	"log/slog"
	"strings"
	"testing"
)

// captureAgentSlog swaps the slog default for a buffer-backed handler and
// restores the real one on cleanup. Capturing slog.Default() inside the
// cleanup instead would restore the buffer handler, silencing later tests.
func captureAgentSlog(t *testing.T) *strings.Builder {
	t.Helper()
	var buf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestAssistantReplyMessage_EmptyReplyWarns(t *testing.T) {
	logs := captureAgentSlog(t)

	msg := assistantReplyMessage(Reply{
		Model:        "some-model",
		StopReason:   "stop",
		InputTokens:  1200,
		OutputTokens: 0,
	})

	if msg.Content != "[no content]" {
		t.Errorf("Content = %q, want the placeholder", msg.Content)
	}
	out := logs.String()
	for _, want := range []string{
		"assistant reply carried no text",
		"model=some-model",
		"stop_reason=stop",
		"input_tokens=1200",
		"output_tokens=0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q missing %q", out, want)
		}
	}
}

// A thinking-only reply is still a blank bubble for the user, so it warns —
// but the trace it did produce is worth naming in the log line.
func TestAssistantReplyMessage_ThinkingOnlyWarns(t *testing.T) {
	logs := captureAgentSlog(t)

	assistantReplyMessage(Reply{
		Model:  "some-model",
		Blocks: []ContentBlock{{Type: "thinking", Thinking: "hmm"}},
	})

	if out := logs.String(); !strings.Contains(out, "has_thinking=true") {
		t.Errorf("log %q missing has_thinking=true", out)
	}
}

// A tool-use round legitimately carries no text; warning on it would fire on
// every tool call the agent makes.
func TestAssistantReplyMessage_ToolUseDoesNotWarn(t *testing.T) {
	logs := captureAgentSlog(t)

	assistantReplyMessage(Reply{
		Model:      "some-model",
		StopReason: "tool_use",
		Blocks:     []ContentBlock{{Type: "tool_use", ID: "t1", Name: "terminal"}},
	})

	if out := logs.String(); out != "" {
		t.Errorf("tool_use reply logged %q, want silence", out)
	}
}

func TestAssistantReplyMessage_TextReplyDoesNotWarn(t *testing.T) {
	logs := captureAgentSlog(t)

	if msg := assistantReplyMessage(Reply{Content: "hello"}); msg.Content != "hello" {
		t.Errorf("Content = %q, want %q", msg.Content, "hello")
	}
	if out := logs.String(); out != "" {
		t.Errorf("non-empty reply logged %q, want silence", out)
	}
}
