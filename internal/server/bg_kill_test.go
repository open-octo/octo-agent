package server

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/tools"
)

// Killing a running background process from the popover must take the
// process down and let the exit hook do the rest: a "cancelled" notice (not
// "success" — the model is told it was killed, not finished) and a badge
// refresh with the row gone.
//
// Deliberately does NOT call wireBackgroundTaskNotices first: a process
// launched from a REST-driven turn (runTurn) lives in the same per-session
// manager but that path never installs the hook, so the handler must wire it
// itself or the kill lands silently.
func TestHandleWSKillBackground_KillsAndBroadcastsCancelled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("spawns a real shell; PowerShell startup is slow and flaky on CI")
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()

	const sid = "bg-kill-test-session"
	defer tools.CloseSessionBackgroundManager(sid)
	conn := subscribedConn(t, srv, sid)

	mgr := tools.SessionBackgroundManager(sid)
	id, err := mgr.Start(context.Background(), "sleep 60", tools.BgModeAsync)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	srv.handleWSKillBackground(sid, id)

	var gotNotice, gotUpdate bool
	deadline := time.After(5 * time.Second)
	for !(gotNotice && gotUpdate) {
		select {
		case b := <-conn.send:
			var ev map[string]any
			if err := json.Unmarshal(b, &ev); err != nil {
				continue
			}
			switch ev["type"] {
			case "background_task_notice":
				gotNotice = true
				if ev["status"] != "cancelled" {
					t.Errorf("notice status = %v, want cancelled", ev["status"])
				}
				if ev["handle_id"] != id {
					t.Errorf("notice handle_id = %v, want %s", ev["handle_id"], id)
				}
			case "background_tasks_update":
				gotUpdate = true
				if ev["running"] != float64(0) {
					t.Errorf("update running = %v, want 0 after kill", ev["running"])
				}
			}
		case <-deadline:
			t.Fatalf("timed out; notice=%v update=%v", gotNotice, gotUpdate)
		}
	}
	if len(mgr.ListRunning()) != 0 {
		t.Errorf("process still listed as running after kill")
	}
}

// An id the manager no longer knows (the process exited between two badge
// broadcasts, so the popover showed a ghost row) must not error out; the
// handler re-broadcasts the live list so the stale row disappears, and no
// notice is fabricated for a kill that never happened.
func TestHandleWSKillBackground_UnknownIDResyncsBadge(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()

	const sid = "bg-kill-unknown-session"
	defer tools.CloseSessionBackgroundManager(sid)
	conn := subscribedConn(t, srv, sid)
	srv.wireBackgroundTaskNotices(sid)

	srv.handleWSKillBackground(sid, "bg_404")

	ev := nextEvent(t, conn)
	if ev["type"] != "background_tasks_update" {
		t.Fatalf("type = %v, want background_tasks_update", ev["type"])
	}
	if ev["running"] != float64(0) {
		t.Errorf("running = %v, want 0", ev["running"])
	}
	select {
	case b := <-conn.send:
		t.Fatalf("unexpected second event: %s", b)
	case <-time.After(200 * time.Millisecond):
	}
}

// The raw WS frame the browser sends (ws.ts killBackground) must reach the
// handler through dispatch with both JSON tags decoded — the wire contract is
// only visible from this end.
func TestHandleWSKillBackground_DispatchFromRawJSON(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()

	const sid = "bg-kill-dispatch-session"
	defer tools.CloseSessionBackgroundManager(sid)
	conn := subscribedConn(t, srv, sid)

	raw := []byte(`{"type":"kill_background","session_id":"bg-kill-dispatch-session","handle_id":"bg_404"}`)
	conn.dispatch("kill_background", raw)

	// Unknown id → the resync broadcast, proving session_id was decoded (the
	// broadcast is per-session) and the handler ran at all.
	ev := nextEvent(t, conn)
	if ev["type"] != "background_tasks_update" {
		t.Fatalf("type = %v, want background_tasks_update", ev["type"])
	}
	if ev["session_id"] != sid {
		t.Errorf("session_id = %v, want %s", ev["session_id"], sid)
	}
}

// An empty handle id is a malformed message, not a request: nothing is killed
// and nothing is broadcast.
func TestHandleWSKillBackground_EmptyIDIsNoop(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()

	const sid = "bg-kill-empty-session"
	defer tools.CloseSessionBackgroundManager(sid)
	conn := subscribedConn(t, srv, sid)

	srv.handleWSKillBackground(sid, "")

	select {
	case b := <-conn.send:
		t.Fatalf("unexpected event: %s", b)
	case <-time.After(200 * time.Millisecond):
	}
}
