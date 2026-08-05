package server

import (
	"context"
	"testing"

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
