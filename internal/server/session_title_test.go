package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
)

func TestIsAutoNamePlaceholder(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		{"", true},
		{"  ", true},
		{"Session 1", true},
		{"Session 42", true},
		{"Session", false},
		{"修复登录问题", false},
		{"My Session 2", false},
	}
	for _, c := range cases {
		if got := isAutoNamePlaceholder(c.title); got != c.want {
			t.Errorf("isAutoNamePlaceholder(%q) = %v, want %v", c.title, got, c.want)
		}
	}
}

// TestDoAgentTurn_GeneratesSessionTitle is the regression guard for web
// sessions never getting an auto-generated title. Two historical gaps: only
// the TUI called GenerateTitle, and web sessions are created with a
// "Session N" placeholder title that blocked the untitled-only gate.
func TestDoAgentTurn_GeneratesSessionTitle(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	// Born with the frontend's auto-assigned placeholder, like every session
	// created via POST /api/sessions.
	sess := agent.NewSession("stub-model", "")
	sess.Title = "Session 2"
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

// TestListSessionsBrief_ReflectsGeneratedTitle guards the REST fallback path
// used by the web UI when the live session_renamed broadcast is missed. The
// sidebar should be able to refresh from listSessions and see the new title.
func TestListSessionsBrief_ReflectsGeneratedTitle(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	sess.Title = "Session 3"
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Drain the rename broadcast so we know title generation finished.
	conn := &wsConn{
		hub:        srv.wsHub,
		send:       make(chan []byte, 256),
		subscribed: map[string]struct{}{},
	}
	srv.wsHub.register <- conn

	srv.doAgentTurn(sess, "hello there", nil, nil)

	deadline := time.After(5 * time.Second)
wait:
	for {
		select {
		case b := <-conn.send:
			var ev map[string]any
			if err := json.Unmarshal(b, &ev); err != nil {
				continue
			}
			if ev["type"] == "session_renamed" && ev["session_id"] == sess.ID {
				break wait
			}
		case <-deadline:
			t.Fatal("no session_renamed broadcast — title was never generated")
		}
	}

	// listSessionsBrief is what the frontend REST fallback calls. It must
	// report the generated title, not the placeholder.
	brief := srv.listSessionsBrief()
	if len(brief) != 1 {
		t.Fatalf("listSessionsBrief returned %d sessions, want 1", len(brief))
	}
	if brief[0].Name != "stub reply" {
		t.Errorf("Name = %q, want %q", brief[0].Name, "stub reply")
	}
}
