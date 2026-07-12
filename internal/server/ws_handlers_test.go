package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
)

// A turn runs in a bare goroutine outside net/http's per-request recover; an
// unrecovered panic there would crash the whole serve process (and, in the
// in-process desktop app, take every session down with it). recoverTurn must
// catch it and push the end-of-turn frames the panic skipped, so the composer
// leaves its "thinking" state instead of hanging.
func TestRecoverTurn_RecoversPanicAndUnsticksUI(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	const sid = "sess-panic"
	conn := &wsConn{send: make(chan []byte, 16), subscribed: map[string]struct{}{}}
	srv.wsHub.subscribe(conn, sid)

	// A panicking turn body must not propagate — if recoverTurn didn't recover,
	// this goroutine's panic would crash the test process.
	func() {
		defer srv.recoverTurn(sid)
		panic("boom in a turn")
	}()
	// Reaching here proves the panic was recovered.

	got := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for len(got) < 3 {
		select {
		case b := <-conn.send:
			var m map[string]any
			if json.Unmarshal(b, &m) == nil {
				if typ, _ := m["type"].(string); typ != "" {
					got[typ] = true
				}
			}
		case <-deadline:
			t.Fatalf("did not receive the unstick frames after a recovered turn panic; got %v", got)
		}
	}
	for _, want := range []string{"error", "complete", "session_update"} {
		if !got[want] {
			t.Errorf("missing %q frame after a recovered turn panic (UI would stay stuck)", want)
		}
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
