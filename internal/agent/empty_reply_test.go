package agent

import (
	"context"
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
	}, false)

	if msg.Content != "[no content]" {
		t.Errorf("Content = %q, want the placeholder", msg.Content)
	}
	out := logs.String()
	for _, want := range []string{
		"assistant reply carried no text",
		"model=some-model",
		"stop_reason=stop",
		"carried_text=false",
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
	}, false)

	if out := logs.String(); !strings.Contains(out, "has_thinking=true") {
		t.Errorf("log %q missing has_thinking=true", out)
	}
}

// A turn-end reminder holds text the user has already read and runLoop
// re-attaches it after this append, so the round still warrants a line but
// must not read as the blank-bubble symptom.
func TestAssistantReplyMessage_CarriedTextIsMarked(t *testing.T) {
	logs := captureAgentSlog(t)

	assistantReplyMessage(Reply{Model: "some-model"}, true)

	if out := logs.String(); !strings.Contains(out, "carried_text=true") {
		t.Errorf("log %q missing carried_text=true", out)
	}
}

// Defensive: runLoop's tool_use branch continues before reaching
// assistantReplyMessage, so this can only fire for a provider that returns
// tool_use blocks under some other stop reason. Pin it anyway — a tool round
// has no text by design and must never be reported as an empty reply.
func TestAssistantReplyMessage_ToolUseDoesNotWarn(t *testing.T) {
	logs := captureAgentSlog(t)

	assistantReplyMessage(Reply{
		Model:      "some-model",
		StopReason: "tool_use",
		Blocks:     []ContentBlock{{Type: "tool_use", ID: "t1", Name: "terminal"}},
	}, false)

	if out := logs.String(); out != "" {
		t.Errorf("tool_use reply logged %q, want silence", out)
	}
}

func TestAssistantReplyMessage_TextReplyDoesNotWarn(t *testing.T) {
	logs := captureAgentSlog(t)

	if msg := assistantReplyMessage(Reply{Content: "hello"}, false); msg.Content != "hello" {
		t.Errorf("Content = %q, want %q", msg.Content, "hello")
	}
	if out := logs.String(); out != "" {
		t.Errorf("non-empty reply logged %q, want silence", out)
	}
}

// The cases above call assistantReplyMessage directly, which proves the
// function is right but not that it is wired in. These drive the real loop.

func TestRun_EmptyReplyWarnsThroughTheLoop(t *testing.T) {
	logs := captureAgentSlog(t)

	send := &fakeToolSender{
		replies: []Reply{{Model: "some-model", StopReason: "stop"}},
	}
	a := New(send, "m")

	reply, err := a.Run(context.Background(), "hello", nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reply.Content != "" {
		t.Errorf("Content = %q, want the empty reply passed through", reply.Content)
	}
	if out := logs.String(); !strings.Contains(out, "assistant reply carried no text") {
		t.Errorf("log %q missing the warning", out)
	}
}

// The guard that matters in practice: an ordinary tool round produces an
// assistant message with no text of its own, and must stay silent.
func TestRun_ToolRoundStaysSilent(t *testing.T) {
	logs := captureAgentSlog(t)

	send := &fakeToolSender{
		replies: []Reply{
			{
				StopReason: "tool_use",
				Blocks: []ContentBlock{
					NewToolUseBlock("call-1", "bash", map[string]any{"command": "echo hi"}),
				},
			},
			{Content: "the output was: hi", StopReason: "end_turn"},
		},
	}
	exec := &fakeExecutor{results: map[string]string{"bash": "hi"}}
	a := New(send, "m")

	if _, err := a.Run(context.Background(), "run echo hi", []ToolDefinition{{Name: "bash"}}, exec); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out := logs.String(); out != "" {
		t.Errorf("tool round logged %q, want silence", out)
	}
}
