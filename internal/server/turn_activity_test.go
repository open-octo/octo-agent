package server

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/scheduler"
)

// TestDoAgentTurn_BroadcastsRunningActivityPair: the sidebar's running spinner
// on tabs NOT subscribed to the session is driven by the global
// session_activity turn_started/turn_ended pair (session_update carries status
// only to subscribers). turn_ended rides a defer, so the pair must arrive even
// when the turn errors out — otherwise a failed turn leaves the session stuck
// "running" in every other tab's sidebar. Uses erroringSender to exercise
// exactly that path.
func TestDoAgentTurn_BroadcastsRunningActivityPair(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.sender = erroringSender{}
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]queuedTurn)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	sess.Title = "fixed title"
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Registered but never subscribed — only a global broadcast can reach it.
	other := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.register <- other

	srv.doAgentTurn(sess, "question", nil, nil)

	started := drainForEvent(t, other, func(ev map[string]any) bool {
		return ev["type"] == "session_activity" && ev["kind"] == "turn_started"
	})
	if started["session_id"] != sess.ID {
		t.Fatalf("turn_started session_id = %v, want %v", started["session_id"], sess.ID)
	}
	ended := drainForEvent(t, other, func(ev map[string]any) bool {
		return ev["type"] == "session_activity" && ev["kind"] == "turn_ended"
	})
	if ended["session_id"] != sess.ID {
		t.Fatalf("turn_ended session_id = %v, want %v", ended["session_id"], sess.ID)
	}
}

// TestRunTask_BroadcastsRunningActivityPair: the scheduled-task turn body is a
// separate runner from doAgentTurn but registers an interrupt the same way, so
// sessionStatus reports "running" for its duration — and cron's session_created
// broadcast makes every tab refresh its list mid-run and seed that status.
// Without the same global pair, an unsubscribed tab's spinner would stick
// forever once the task finished.
func TestRunTask_BroadcastsRunningActivityPair(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.sender = erroringSender{}
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]queuedTurn)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sessionID, err := srv.CreateSession(scheduler.Task{Name: "t"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Registered but never subscribed — only a global broadcast can reach it.
	other := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.register <- other

	if _, err := srv.RunTask(context.Background(), scheduler.Task{Name: "t", Prompt: "will fail", SessionID: sessionID}); err == nil {
		t.Fatal("RunTask should surface the provider error")
	}

	started := drainForEvent(t, other, func(ev map[string]any) bool {
		return ev["type"] == "session_activity" && ev["kind"] == "turn_started"
	})
	if started["session_id"] != sessionID {
		t.Fatalf("turn_started session_id = %v, want %v", started["session_id"], sessionID)
	}
	ended := drainForEvent(t, other, func(ev map[string]any) bool {
		return ev["type"] == "session_activity" && ev["kind"] == "turn_ended"
	})
	if ended["session_id"] != sessionID {
		t.Fatalf("turn_ended session_id = %v, want %v", ended["session_id"], sessionID)
	}
}

// TestDoAgentTurn_WritesPrecedeComplete: `complete` tells the client the
// turn's content is settled — it carries the reply's persisted message_index
// — so every end-of-turn file write (title adoption, context-usage persist)
// must land BEFORE it. The sidebar's unread watermark compares the session
// file's mtime against the last moment the user could have read the reply; a
// write after `complete` pushes the mtime past the turn's visible completion
// and resurfaces as a phantom unread dot for anyone whose window closed (or
// whose webview was suspended) before turn_ended arrived. The adoption's
// session_renamed re-broadcast rides the same code path as the write, so its
// order against complete is the observable proxy for "the write came first".
//
// The mid-turn title generation is pre-claimed away: its live session_renamed
// broadcast precedes complete on every ordering and would make the assertion
// vacuous.
func TestDoAgentTurn_WritesPrecedeComplete(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.sender = stubSender{}
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]queuedTurn)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	if !agent.IsAutoNamePlaceholder(sess.Title) {
		t.Fatalf("test needs the placeholder title, got %q", sess.Title)
	}
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Occupy the generation claim so doAgentTurn never spawns its own title
	// goroutine, then hand the turn a pending title to adopt directly.
	if !srv.claimTitleGeneration(sess.ID) {
		t.Fatal("pre-claim: title generation unexpectedly already in flight")
	}
	srv.storePendingTitle(sess.ID, "Adopted Title")

	// Subscribed to the session, so both the per-session `complete` and the
	// global `session_renamed` land in this conn, in broadcast order.
	sub := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.register <- sub
	srv.wsHub.subscribe(sub, sess.ID)

	srv.doAgentTurn(sess, "question", nil, nil)
	// Balance the pre-claim above: mustServer's cleanup waits (up to 10s) for
	// titlePending to drain, and nothing else releases this claim.
	srv.releaseTitleGeneration(sess.ID)

	renamedAt, completeAt := -1, -1
	deadline := time.After(5 * time.Second)
	for i := 0; completeAt < 0; i++ {
		select {
		case raw := <-sub.send:
			var ev map[string]any
			if err := json.Unmarshal(raw, &ev); err != nil {
				t.Fatalf("decode event: %v", err)
			}
			switch ev["type"] {
			case "session_renamed":
				if renamedAt < 0 {
					renamedAt = i
				}
			case "complete":
				completeAt = i
			}
		case <-deadline:
			t.Fatal("timed out waiting for the complete event")
		}
	}
	if renamedAt < 0 {
		t.Fatal("no session_renamed broadcast — the pending title was never adopted")
	}
	if renamedAt > completeAt {
		t.Fatalf("title adoption (event #%d) landed after complete (#%d): end-of-turn writes must precede the complete broadcast", renamedAt, completeAt)
	}


	// And the write itself must be on disk, not just broadcast.
	p, err := sess.SavePath()
	if err != nil {
		t.Fatalf("SavePath: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	if !strings.Contains(string(b), "Adopted Title") {
		t.Fatal("adopted title missing from the persisted transcript")
	}
}
