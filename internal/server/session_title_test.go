package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
)

// TestDoAgentTurn_GeneratesSessionTitle is the regression guard for web
// sessions never getting an auto-generated title: only the TUI called
// GenerateTitle after the first turn, so web-created sessions stayed on the
// first-message-snippet fallback forever.
func TestDoAgentTurn_GeneratesSessionTitle(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Registered but NOT subscribed to the session: session_renamed must be a
	// global broadcast (the sidebar lists every session in every tab).
	conn := &wsConn{
		hub:        srv.wsHub,
		send:       make(chan []byte, 256),
		subscribed: map[string]struct{}{},
	}
	srv.wsHub.register <- conn

	srv.doAgentTurn(sess, "hello there", nil, nil)

	deadline := time.After(5 * time.Second)
	for {
		select {
		case b := <-conn.send:
			var ev map[string]any
			if err := json.Unmarshal(b, &ev); err != nil {
				continue
			}
			if ev["type"] != "session_renamed" {
				continue
			}
			if ev["session_id"] != sess.ID {
				t.Errorf("session_id = %v, want %s", ev["session_id"], sess.ID)
			}
			if ev["name"] != "stub reply" {
				t.Errorf("name = %v, want %q", ev["name"], "stub reply")
			}
			// The title must also be persisted.
			loaded, err := agent.LoadSession(sess.ID)
			if err != nil {
				t.Fatalf("reload: %v", err)
			}
			if loaded.Title != "stub reply" {
				t.Errorf("persisted Title = %q, want %q", loaded.Title, "stub reply")
			}
			return
		case <-deadline:
			t.Fatal("no session_renamed broadcast — title was never generated")
		}
	}
}
