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

// An idle session (no live Agent) must report the real context-token count its
// last turn persisted, so a fresh subscribe (page refresh / reconnect) shows
// the same value the turn-end broadcast did — not a transcript estimate that
// omits the system-prompt/tools overhead and can round to 0 (sending no frame
// at all, leaving the composer's Context bar stale). This is what makes the bar
// correct across switching between multiple idle sessions.
func TestSendContextUsage_UsesPersistedTokens(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	// A persisted, idle session (no entry in sessionAgents) carrying a real
	// last-turn token count. 6400 tokens is ~5% of the 128k default window.
	sess := agent.NewSession("stub-model", "")
	sess.LastContextTokens = 6400
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}

	conn := &wsConn{send: make(chan []byte, 4), subscribed: map[string]struct{}{}}
	srv.sendContextUsage(sess.ID, conn)

	select {
	case b := <-conn.send:
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if m["type"] != "session_update" {
			t.Fatalf("type = %v, want session_update", m["type"])
		}
		if cu, _ := m["context_usage"].(float64); cu != 5 {
			t.Fatalf("context_usage = %v, want 5 (6400/128000)", cu)
		}
		if ct, _ := m["context_tokens"].(float64); ct != 6400 {
			t.Fatalf("context_tokens = %v, want 6400", ct)
		}
	default:
		t.Fatal("sendContextUsage sent no frame despite a persisted token count")
	}

	// The count must survive a reload from disk (it rides the transcript).
	reloaded, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.LastContextTokens != 6400 {
		t.Fatalf("reloaded LastContextTokens = %d, want 6400", reloaded.LastContextTokens)
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
