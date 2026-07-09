package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

func TestBgNoticeStatus(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"exited: 0", "success"},
		{"exited: exit status 1", "failed"},
		{"exited: signal: killed", "cancelled"},
		{"exited: something else", "failed"},
	}
	for _, c := range cases {
		if got := bgNoticeStatus(c.in); got != c.want {
			t.Errorf("bgNoticeStatus(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBackgroundTasksUpdate_Payload(t *testing.T) {
	now := time.Now()
	infos := []tools.BgInfo{
		{ID: "bg_1", Command: "sleep 30", Start: now.Add(-12 * time.Second), Status: "running"},
		{ID: "bg_2", Command: "tail -f log", Start: now.Add(-3 * time.Second), Status: "running"},
	}
	ev := backgroundTasksUpdate("sess-1", infos, now)

	if ev.Type != "background_tasks_update" {
		t.Errorf("Type = %q", ev.Type)
	}
	if ev.SessionID != "sess-1" {
		t.Errorf("SessionID = %q", ev.SessionID)
	}
	if ev.Running != 2 || len(ev.Tasks) != 2 {
		t.Fatalf("Running = %d, Tasks = %d, want 2/2", ev.Running, len(ev.Tasks))
	}
	if ev.Tasks[0].HandleID != "bg_1" || ev.Tasks[0].Command != "sleep 30" || ev.Tasks[0].Elapsed != 12 {
		t.Errorf("Tasks[0] = %+v", ev.Tasks[0])
	}

	// Empty list must still marshal with running 0 and a non-null tasks array
	// so the frontend hides the badge instead of choking on null.
	b, err := json.Marshal(backgroundTasksUpdate("sess-1", nil, now))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if raw["running"] != float64(0) {
		t.Errorf("running = %v, want 0", raw["running"])
	}
	if _, ok := raw["tasks"].([]any); !ok {
		t.Errorf("tasks = %v (%T), want JSON array", raw["tasks"], raw["tasks"])
	}
}

// TestWireBackgroundTaskNotices_BroadcastsExit is the regression guard for the
// web-UI "background tasks invisible" gap: the server defined the
// background_task_notice / background_tasks_update event types but never
// emitted them, so the frontend badge and notices never appeared.
func TestWireBackgroundTaskNotices_BroadcastsExit(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()

	const sid = "bg-notice-test-session"
	defer tools.CloseSessionBackgroundManager(sid)

	conn := &wsConn{
		hub:        srv.wsHub,
		send:       make(chan []byte, 256),
		subscribed: map[string]struct{}{},
	}
	srv.wsHub.register <- conn
	srv.wsHub.subscribe(conn, sid)

	srv.wireBackgroundTaskNotices(sid)

	// Trigger the exit hook directly rather than starting a real shell process.
	// A real process requires PowerShell on Windows (slow startup, flaky on CI).
	// wireBackgroundTaskNotices is what we're testing here — not the shell.
	tools.SessionBackgroundManager(sid).FireExitHook(tools.BgExit{
		ID:      "bg_1",
		Command: "echo done",
		Status:  "exited: 0",
	})

	var gotNotice, gotUpdate bool
	deadline := time.After(3 * time.Second)
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
				if ev["status"] != "success" {
					t.Errorf("notice status = %v, want success", ev["status"])
				}
				if ev["command"] != "echo done" {
					t.Errorf("notice command = %v", ev["command"])
				}
				if ev["session_id"] != sid {
					t.Errorf("notice session_id = %v", ev["session_id"])
				}
			case "background_tasks_update":
				gotUpdate = true
				if ev["running"] != float64(0) {
					t.Errorf("update running = %v, want 0 after exit", ev["running"])
				}
			}
		case <-deadline:
			t.Fatalf("timed out; notice=%v update=%v", gotNotice, gotUpdate)
		}
	}
}

// notifyAgentBgExit must reach the model on both paths: the running Agent's
// Inbox mid-turn, the steer queue while idle. Parity with the CLI/TUI's
// SetBackgroundOnExit → Inbox wiring.
func TestNotifyAgentBgExit_MidTurnGoesToInbox(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	a := agent.New(&recordingSender{}, "stub-model")
	srv.sessionAgentsMu.Lock()
	srv.sessionAgents["sess-1"] = a
	srv.sessionAgentsMu.Unlock()

	srv.notifyAgentBgExit("sess-1", tools.BgExit{ID: "bg_1", Command: "make build", Status: "exited: 0", NewOutput: "done"})

	items := a.Inbox.Drain()
	if len(items) != 1 || !strings.Contains(items[0].Text, "[BACKGROUND COMPLETED]") || !strings.Contains(items[0].Text, "bg_1") {
		t.Fatalf("inbox = %+v, want one bg note", items)
	}
	// Nothing should have leaked into the idle steer queue.
	if leftover := srv.drainSteer("sess-1"); len(leftover) != 0 {
		t.Errorf("steer queue = %+v, want empty", leftover)
	}
}

func TestNotifyAgentBgExit_IdleGoesToSteerQueue(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	srv.notifyAgentBgExit("sess-1", tools.BgExit{ID: "bg_2", Command: "make test", Status: "exited: exit status 1"})

	items := srv.drainSteer("sess-1")
	if len(items) != 1 || !strings.Contains(items[0].Text, "[BACKGROUND COMPLETED]") || !strings.Contains(items[0].Text, "bg_2") {
		t.Fatalf("steer queue = %+v, want one bg note", items)
	}
}

// An idle-time note must not just sit in the steer queue — it kicks a turn so
// the model reacts immediately (parity with the TUI's idle auto-turn). The
// kicked turn consumes the note; the reminder never renders as a user bubble
// (doAgentTurn strips <system-reminder> spans from the broadcast).
func TestDeliverModelNote_IdleKicksTurn(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.wsHub = newWSHub()
	srv.turnRunning = map[string]bool{}
	srv.liveStates = map[string]*sessionLiveState{}
	srv.interrupts = map[string]context.CancelFunc{}

	sess := agent.NewSession("stub-model", "")
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	srv.deliverModelNote(sess.ID, "<system-reminder>\n[BACKGROUND COMPLETED]\nBackground process bg_1 (`make build`) exited: 0.\n</system-reminder>")

	// The kicked turn runs asynchronously; wait for it to finish.
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu := srv.sessionTurnLock(sess.ID)
		mu.Lock()
		running := srv.turnRunning[sess.ID]
		mu.Unlock()
		if !running {
			// Either finished or never started — check results below.
			loaded, err := agent.LoadSession(sess.ID)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if len(loaded.Messages) >= 2 {
				if leftover := srv.drainSteer(sess.ID); len(leftover) != 0 {
					t.Errorf("steer queue = %+v, want drained", leftover)
				}
				return // turn ran: user note + assistant reply persisted
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("kicked turn never completed")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestKickIdleSteerTurn_SkipsWhenBoundToOtherEntry: if another entry has
// taken over the session while we were idle, the idle follow-up turn must not
// run; the note stays in the steer queue for the owning entry to drain.
func TestKickIdleSteerTurn_SkipsWhenBoundToOtherEntry(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.wsHub = newWSHub()
	srv.turnRunning = map[string]bool{}
	srv.liveStates = map[string]*sessionLiveState{}
	srv.interrupts = map[string]context.CancelFunc{}

	sess := agent.NewSession("stub-model", "")
	sess.BoundEntry = agent.EntryCLI
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	note := "<system-reminder>\n[BACKGROUND COMPLETED]\nBackground process bg_1 (`make build`) exited: 0.\n</system-reminder>"
	srv.enqueueSteer(sess.ID, agent.InboxItem{Text: note})
	srv.kickIdleSteerTurn(sess.ID)

	// Give any goroutine time to start.
	time.Sleep(100 * time.Millisecond)

	mu := srv.sessionTurnLock(sess.ID)
	mu.Lock()
	running := srv.turnRunning[sess.ID]
	mu.Unlock()
	if running {
		t.Fatal("idle turn should not start when session is bound to another entry")
	}
	if leftover := srv.drainSteer(sess.ID); len(leftover) != 1 {
		t.Errorf("steer queue = %d items, want 1 (preserved for the owning entry)", len(leftover))
	}
}

// prepareToolTurn must hand back the SAME session-scoped manager on every
// turn of a session (async mode), so spawns survive turn boundaries — and a
// sid-less context still gets the old per-turn synchronous manager.
func TestPrepareToolTurn_SessionScopedAsyncManager(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Tools: true})
	a := agent.New(&stubSender{}, "stub-model")

	ctx := context.WithValue(context.Background(), ctxKeySessionID{}, "sess-mgr-test")
	t.Cleanup(func() { tools.CloseSessionSubAgentManager("sess-mgr-test") })

	_, _, m1, _, err := srv.prepareToolTurn(ctx, a, nil)
	if err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	_, _, m2, _, err := srv.prepareToolTurn(ctx, a, nil)
	if err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	if m1 != m2 {
		t.Error("same session should reuse one sub-agent manager across turns")
	}
	if m1.Synchronous() {
		t.Error("session-scoped manager should be async")
	}

	_, _, anon, _, err := srv.prepareToolTurn(context.Background(), a, nil)
	if err != nil {
		t.Fatalf("prepareToolTurn (no sid): %v", err)
	}
	if !anon.Synchronous() {
		t.Error("sid-less context should fall back to a synchronous manager")
	}
	if anon == m1 {
		t.Error("sid-less manager must not be the session-scoped one")
	}
}

// prepareToolTurn used to build a fresh DefaultRegistry (and so a fresh
// ReadTracker) on every call, so a file read_file'd in one turn looked
// unread to write_file/edit_file in the next turn of the SAME session. The
// executor must now share a session-scoped tracker across turns, while a
// different session id must not see another session's reads.
func TestPrepareToolTurn_ReadTrackerPersistsAcrossTurns(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Tools: true})
	a := agent.New(&stubSender{}, "stub-model")

	sid := "sess-read-tracker-turn-test"
	ctx := context.WithValue(context.Background(), ctxKeySessionID{}, sid)
	t.Cleanup(func() {
		tools.CloseSessionSubAgentManager(sid)
		tools.CloseSessionReadTracker(sid)
	})

	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Turn 1: fresh executor from prepareToolTurn reads the file.
	_, exec1, _, _, err := srv.prepareToolTurn(ctx, a, nil)
	if err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	if _, err := exec1.Execute(ctx, "read_file", map[string]any{"path": p}); err != nil {
		t.Fatalf("read_file: %v", err)
	}

	// Turn 2: a NEW executor (as prepareToolTurn builds every turn) must
	// still honor the read recorded in turn 1.
	_, exec2, _, _, err := srv.prepareToolTurn(ctx, a, nil)
	if err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	if _, err := exec2.Execute(ctx, "edit_file", map[string]any{
		"path": p, "old_string": "package x", "new_string": "package y",
	}); err != nil {
		t.Errorf("edit in a later turn of the same session should see the earlier turn's read: %v", err)
	}

	// A different session must not inherit that read — reuse the SAME path p
	// (already read by session sid above) rather than an untouched file, so
	// this actually exercises isolation instead of trivially failing because
	// nobody ever read the path. CheckWritable runs before the tool body, so
	// this fails on "not been read" before old_string matching is even
	// reached, regardless of p's content having changed in turn 2.
	otherSid := "sess-read-tracker-other"
	otherCtx := context.WithValue(context.Background(), ctxKeySessionID{}, otherSid)
	t.Cleanup(func() {
		tools.CloseSessionSubAgentManager(otherSid)
		tools.CloseSessionReadTracker(otherSid)
	})
	_, exec3, _, _, err := srv.prepareToolTurn(otherCtx, a, nil)
	if err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	_, err = exec3.Execute(otherCtx, "edit_file", map[string]any{
		"path": p, "old_string": "package y", "new_string": "package z",
	})
	if err == nil || !strings.Contains(err.Error(), "not been read") {
		t.Errorf("a different session should not inherit another session's read, got %v", err)
	}
}

// TestPrepareToolTurn_AdvertisesSubAgentAndWorkflow guards the core fix for
// cron/server sessions: after prepareToolTurn, tools.DefaultToolsForCtx(ctx,
// ...) — using the ctx prepareToolTurn returns — must include both sub_agent
// and workflow so the model can invoke them, and (#1133) it must do so
// without writing to the process-global spawner/sub-agent-manager slots,
// since those are only meaningful for a single-session CLI process.
func TestPrepareToolTurn_AdvertisesSubAgentAndWorkflow(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// Preserve and restore global state so this test doesn't leak to others.
	prevSpawner := tools.ActiveSpawner()
	prevMgr := tools.DefaultSubAgentManager()
	t.Cleanup(func() {
		tools.SetSpawner(prevSpawner)
		tools.SetDefaultSubAgentManager(prevMgr)
	})

	// Start from a clean slate: nothing but the ctx-scoped manager
	// prepareToolTurn stamps into ctx should make the gate pass.
	tools.SetSpawner(nil)
	tools.SetDefaultSubAgentManager(nil)

	srv := mustServer(t, Config{Tools: true})
	a := agent.New(&stubSender{}, "stub-model")
	ctx := context.WithValue(context.Background(), ctxKeySessionID{}, "adv-test")
	t.Cleanup(func() { tools.CloseSessionSubAgentManager("adv-test") })

	turnCtx, _, _, cleanup, err := srv.prepareToolTurn(ctx, a, nil)
	if err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	defer cleanup()

	names := toolNames(tools.DefaultToolsForCtx(turnCtx, a.Model))
	if !slices.Contains(names, "sub_agent") {
		t.Errorf("DefaultToolsForCtx missing sub_agent; got %v", names)
	}
	if !slices.Contains(names, "workflow") {
		t.Errorf("DefaultToolsForCtx missing workflow; got %v", names)
	}

	// #1133's whole point: the turn must not have installed itself into the
	// process-global slots to get there.
	if tools.ActiveSpawner() != nil {
		t.Error("prepareToolTurn must not write the process-global spawner")
	}
	if tools.DefaultSubAgentManager() != nil {
		t.Error("prepareToolTurn must not write the process-global sub-agent manager")
	}
}

// TestPrepareToolTurn_DoesNotTouchGlobalSpawner (#1133) replaces the old
// swap-and-restore contract: prepareToolTurn must leave the process-global
// spawner/sub-agent-manager slots completely alone — not set them and
// restore them, just never touch them — since a multi-session server can't
// safely mutate process-global state per turn.
func TestPrepareToolTurn_DoesNotTouchGlobalSpawner(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	prevSpawner := tools.ActiveSpawner()
	prevMgr := tools.DefaultSubAgentManager()
	t.Cleanup(func() {
		tools.SetSpawner(prevSpawner)
		tools.SetDefaultSubAgentManager(prevMgr)
	})

	// Pre-seed the global slots with sentinel values so any write during the
	// turn (or its cleanup) would be observable.
	sentinelMgr := tools.NewSubAgentManager(nil)
	tools.SetSpawner(nil)
	tools.SetDefaultSubAgentManager(sentinelMgr)

	srv := mustServer(t, Config{Tools: true})
	a := agent.New(&stubSender{}, "stub-model")
	ctx := context.WithValue(context.Background(), ctxKeySessionID{}, "cleanup-test")
	t.Cleanup(func() { tools.CloseSessionSubAgentManager("cleanup-test") })

	_, _, _, cleanup, err := srv.prepareToolTurn(ctx, a, nil)
	if err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	if tools.ActiveSpawner() != nil {
		t.Error("prepareToolTurn wrote the process-global spawner during the turn")
	}
	if tools.DefaultSubAgentManager() != sentinelMgr {
		t.Error("prepareToolTurn overwrote the process-global sub-agent manager during the turn")
	}

	cleanup()

	if tools.ActiveSpawner() != nil {
		t.Error("cleanup should not have touched the process-global spawner")
	}
	if tools.DefaultSubAgentManager() != sentinelMgr {
		t.Error("cleanup should not have touched the process-global sub-agent manager")
	}
}

func toolNames(defs []agent.ToolDefinition) []string {
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	return names
}

// toolInputSpawner emits a "tool" SubAgentEvent carrying ToolInput, the way
// internal/app/spawner.go's real spawner does when a sub-agent dispatches a
// tool call.
type toolInputSpawner struct{}

func (toolInputSpawner) Spawn(ctx context.Context, req tools.SpawnRequest) (tools.SpawnResult, error) {
	if sink := tools.SubAgentEventSink(ctx); sink != nil {
		sink(tools.SubAgentEvent{Kind: "tool", ToolName: "read_file", ToolInput: map[string]any{"path": "go.mod"}})
	}
	return tools.SpawnResult{Reply: "ok"}, nil
}

func (toolInputSpawner) Continue(_ context.Context, _, _ string) (tools.SpawnResult, error) {
	return tools.SpawnResult{}, nil
}

// TestPrepareToolTurn_SubAgentToolEventCarriesToolInput guards the web
// sub-agent panel's tool-arguments display: the frontend (ChatView.svelte's
// sub_agent_event handler, stores.ts's applySubAgentEvent, SubAgentsCard's
// tool-input rendering) has supported showing a tool's input all along, and
// tools.SubAgentEvent has carried ToolInput since it was added — but the
// server's WS broadcast in prepareToolTurn's SubAgentOnEvent hook dropped
// the field, so the argument list never had anything to render.
func TestPrepareToolTurn_SubAgentToolEventCarriesToolInput(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: true})
	srv.initWS()

	const sid = "subagent-tool-input-test"
	// Pre-seed the session's manager with a fake spawner; prepareToolTurn's
	// SessionSubAgentManager call reuses this cached instance and only wires
	// the real broadcast callback onto it (mkSpawner runs once per session).
	tools.SessionSubAgentManager(sid, func() tools.Spawner { return toolInputSpawner{} })
	t.Cleanup(func() { tools.CloseSessionSubAgentManager(sid) })

	a := agent.New(&stubSender{}, "stub-model")
	ctx := context.WithValue(context.Background(), ctxKeySessionID{}, sid)
	_, _, mgr, cleanup, err := srv.prepareToolTurn(ctx, a, nil)
	if err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	defer cleanup()

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.register <- conn
	srv.wsHub.subscribe(conn, sid)

	if _, err := mgr.Start(tools.SpawnRequest{Description: "d", Prompt: "p"}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// 3s intermittently timed out on loaded/slow CI runners (observed on
	// windows-latest) even though the broadcast wiring itself is
	// synchronous and this test passes reliably (100/100) in isolation —
	// the flake is scheduling latency under CI load, not a logic race.
	deadline := time.After(15 * time.Second)
	for {
		select {
		case b := <-conn.send:
			var ev map[string]any
			if err := json.Unmarshal(b, &ev); err != nil {
				continue
			}
			if ev["type"] != "sub_agent_event" || ev["kind"] != "tool" {
				continue
			}
			input, ok := ev["tool_input"].(map[string]any)
			if !ok || input["path"] != "go.mod" {
				t.Fatalf("tool_input = %v, want {path: go.mod}", ev["tool_input"])
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for the tool sub_agent_event")
		}
	}
}
