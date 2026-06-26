package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
)

// TestHandleWSUserMessage_BoundToOtherEmitsSendRejected guards the web UI's
// optimistic-send rollback: when the session is already bound to another entry
// (e.g. the TUI), handleWSUserMessage must emit send_rejected so the frontend
// can drop the pending bubble and restore the streaming flag.
func TestHandleWSUserMessage_BoundToOtherEmitsSendRejected(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	if _, _, err := sess.Bind(agent.EntryTUI, false); err != nil {
		t.Fatalf("bind to TUI: %v", err)
	}
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	conn := &wsConn{
		hub:        srv.wsHub,
		send:       make(chan []byte, 256),
		subscribed: map[string]struct{}{},
	}
	srv.wsHub.register <- conn
	srv.wsHub.subscribe(conn, sess.ID)

	srv.handleWSUserMessage(conn, &wsMsgUserMessage{
		SessionID: sess.ID,
		Content:   json.RawMessage(`"hello from web"`),
	})

	var rejected bool
	deadline := time.After(2 * time.Second)
drain:
	for {
		select {
		case b := <-conn.send:
			var ev map[string]any
			if err := json.Unmarshal(b, &ev); err != nil {
				continue
			}
			if typ, _ := ev["type"].(string); typ == "send_rejected" {
				rejected = true
				if sid, _ := ev["session_id"].(string); sid != sess.ID {
					t.Errorf("send_rejected session_id = %q, want %q", sid, sess.ID)
				}
				break drain
			}
		case <-deadline:
			break drain
		}
	}
	if !rejected {
		t.Fatal("expected send_rejected event when session is bound to another entry")
	}
}
