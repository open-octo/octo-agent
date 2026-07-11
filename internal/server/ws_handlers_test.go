package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

// After a turn ends the agent is dropped from sessionAgents, but its exact
// context-token count must survive as the warmAgent so a fresh subscribe (a
// page refresh / reconnect) reports the real value instead of nothing. Without
// the warm agent, sendContextUsage falls back to a transcript estimate that can
// round to 0 and then sends no frame at all — the composer's Context bar stays
// stale until the next turn.
func TestSendContextUsage_UsesWarmAgent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	// An idle agent (not in sessionAgents) with enough history that its context
	// estimate clears 1% of the default 128k window.
	a := agent.New(stubSender{}, "stub-model")
	a.History.Append(agent.NewUserMessage(strings.Repeat("word ", 4000)))
	const sid = "warm-session"
	srv.sessionAgentsMu.Lock()
	srv.warmAgentID = sid
	srv.warmAgent = a
	srv.sessionAgentsMu.Unlock()

	conn := &wsConn{send: make(chan []byte, 4), subscribed: map[string]struct{}{}}
	srv.sendContextUsage(sid, conn)

	select {
	case b := <-conn.send:
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if m["type"] != "session_update" {
			t.Fatalf("type = %v, want session_update", m["type"])
		}
		if cu, _ := m["context_usage"].(float64); cu <= 0 {
			t.Fatalf("context_usage = %v, want > 0 (warm agent's real count)", cu)
		}
	default:
		t.Fatal("sendContextUsage sent no frame despite a warm agent")
	}

	// A session with neither a running nor a warm agent, and nothing on disk,
	// still sends nothing (no misleading 0% frame).
	conn2 := &wsConn{send: make(chan []byte, 4), subscribed: map[string]struct{}{}}
	srv.sendContextUsage("cold-unknown-session", conn2)
	select {
	case <-conn2.send:
		t.Fatal("expected no frame for a cold unknown session")
	default:
	}
}

// lastVisibleUserIdx must land on the typed prompt, not the tool_result
// carrier an agentic turn leaves as its most recent user-role message.
func TestLastVisibleUserIdx_SkipsToolResultCarriers(t *testing.T) {
	msgs := []agent.Message{
		agent.NewUserMessage("fix the bug"), // 0 — the real prompt
		agent.NewToolUseMessage([]agent.ContentBlock{
			agent.NewToolUseBlock("c1", "terminal", map[string]any{"command": "ls"}),
		}), // 1
		agent.NewToolResultMessage([]agent.ContentBlock{
			agent.NewToolResultBlock("c1", "file1", false),
		}), // 2 — user-role carrier, must be skipped
		agent.NewAssistantMessage("done"), // 3
	}
	if got := lastVisibleUserIdx(msgs); got != 0 {
		t.Errorf("lastVisibleUserIdx = %d, want 0 (the typed prompt)", got)
	}
}

func TestLastVisibleUserIdx_SkipsReminderOnly(t *testing.T) {
	msgs := []agent.Message{
		agent.NewUserMessage("real prompt"), // 0
		agent.NewAssistantMessage("ok"),     // 1
		agent.NewUserMessage("<system-reminder>background process exited</system-reminder>"), // 2 — model-facing only
		agent.NewAssistantMessage("noted"),                                                   // 3
	}
	if got := lastVisibleUserIdx(msgs); got != 0 {
		t.Errorf("lastVisibleUserIdx = %d, want 0 (reminder-only messages are not retryable prompts)", got)
	}
}

func TestLastVisibleUserIdx_None(t *testing.T) {
	if got := lastVisibleUserIdx(nil); got != -1 {
		t.Errorf("empty history: got %d, want -1", got)
	}
	onlyAssistant := []agent.Message{agent.NewAssistantMessage("hi")}
	if got := lastVisibleUserIdx(onlyAssistant); got != -1 {
		t.Errorf("no user messages: got %d, want -1", got)
	}
}

// An image-only user message (no text) is still a retryable prompt.
func TestLastVisibleUserIdx_ImageOnly(t *testing.T) {
	img := agent.NewUserMessage("")
	img.Blocks = []agent.ContentBlock{agent.NewImageBlock("image/png", []byte("hi"))}
	msgs := []agent.Message{
		agent.NewUserMessage("first"), // 0
		agent.NewAssistantMessage("ok"),
		img, // 2
		agent.NewAssistantMessage("nice picture"),
	}
	if got := lastVisibleUserIdx(msgs); got != 2 {
		t.Errorf("lastVisibleUserIdx = %d, want 2 (image-only prompt)", got)
	}
}
