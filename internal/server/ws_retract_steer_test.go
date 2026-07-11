package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
)

// subscribedConn registers a fresh wsConn subscribed to sid and returns it so
// the test can read broadcasts off its send channel.
func subscribedConn(t *testing.T, srv *Server, sid string) *wsConn {
	t.Helper()
	conn := &wsConn{
		hub:        srv.wsHub,
		send:       make(chan []byte, 256),
		subscribed: map[string]struct{}{},
	}
	srv.wsHub.register <- conn
	srv.wsHub.subscribe(conn, sid)
	return conn
}

// nextEvent reads the next broadcast off conn, failing the test on timeout.
func nextEvent(t *testing.T, conn *wsConn) map[string]any {
	t.Helper()
	select {
	case b := <-conn.send:
		var ev map[string]any
		if err := json.Unmarshal(b, &ev); err != nil {
			t.Fatalf("unmarshal event: %v", err)
		}
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for broadcast")
		return nil
	}
}

// A steer still sitting in the running Agent's inbox is retractable: the server
// pulls it back out and confirms with steer_retracted so the UI can reload it.
func TestRetractSteer_FromInbox(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()

	const sid = "retract-inbox"
	conn := subscribedConn(t, srv, sid)

	a := agent.New(&recordingSender{}, "stub-model")
	a.Inbox.Enqueue("please also add tests")
	srv.sessionAgentsMu.Lock()
	srv.sessionAgents[sid] = a
	srv.sessionAgentsMu.Unlock()

	srv.handleWSRetractSteer(sid, "pending-1", "please also add tests")

	ev := nextEvent(t, conn)
	if ev["type"] != "steer_retracted" {
		t.Fatalf("type = %v, want steer_retracted", ev["type"])
	}
	if ev["pending_id"] != "pending-1" {
		t.Errorf("pending_id = %v, want pending-1", ev["pending_id"])
	}
	if a.Inbox.HasPending() {
		t.Error("inbox should be empty after retract")
	}
}

// Once the loop has drained the steer into the turn it is committed; a retract
// arriving after that must fail so the UI keeps the bubble instead of stranding
// its text (mirrors the TUI's already-drained fall-through).
func TestRetractSteer_AlreadyDrainedFails(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()

	const sid = "retract-drained"
	conn := subscribedConn(t, srv, sid)

	a := agent.New(&recordingSender{}, "stub-model")
	a.Inbox.Enqueue("steer text")
	_ = a.Inbox.Drain() // loop consumed it
	srv.sessionAgentsMu.Lock()
	srv.sessionAgents[sid] = a
	srv.sessionAgentsMu.Unlock()

	srv.handleWSRetractSteer(sid, "pending-2", "steer text")

	ev := nextEvent(t, conn)
	if ev["type"] != "steer_retract_failed" {
		t.Fatalf("type = %v, want steer_retract_failed", ev["type"])
	}
	if ev["pending_id"] != "pending-2" {
		t.Errorf("pending_id = %v, want pending-2", ev["pending_id"])
	}
}

// With no live Agent registered (steer sitting in the chained-turn queue), the
// retract still works via the queue path.
func TestRetractSteer_FromQueue(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()

	const sid = "retract-queue"
	conn := subscribedConn(t, srv, sid)

	srv.enqueueSteer(sid, agent.InboxItem{Text: "queued steer"})

	srv.handleWSRetractSteer(sid, "pending-3", "queued steer")

	ev := nextEvent(t, conn)
	if ev["type"] != "steer_retracted" {
		t.Fatalf("type = %v, want steer_retracted", ev["type"])
	}
	if leftover := srv.drainSteer(sid); len(leftover) != 0 {
		t.Errorf("steer queue = %+v, want empty after retract", leftover)
	}
}

// End-to-end wire contract: the exact JSON the web ws.ts sends must route
// through dispatch, unmarshal onto wsMsgRetractSteer's fields, and retract.
// Guards against a type-string or json-tag drift between front and back.
func TestRetractSteer_DispatchWireContract(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()

	const sid = "retract-dispatch"
	conn := subscribedConn(t, srv, sid)

	a := agent.New(&recordingSender{}, "stub-model")
	a.Inbox.Enqueue("mid-turn note")
	srv.sessionAgentsMu.Lock()
	srv.sessionAgents[sid] = a
	srv.sessionAgentsMu.Unlock()

	raw := []byte(`{"type":"retract_steer","session_id":"retract-dispatch","pending_id":"pending-9","text":"mid-turn note"}`)
	conn.dispatch("retract_steer", raw)

	ev := nextEvent(t, conn)
	if ev["type"] != "steer_retracted" {
		t.Fatalf("type = %v, want steer_retracted", ev["type"])
	}
	if ev["pending_id"] != "pending-9" {
		t.Errorf("pending_id = %v, want pending-9", ev["pending_id"])
	}
	if a.Inbox.HasPending() {
		t.Error("inbox should be empty after dispatched retract")
	}
}
