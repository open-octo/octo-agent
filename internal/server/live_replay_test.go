package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tasks"
	"github.com/open-octo/octo-agent/internal/tools"
	"github.com/open-octo/octo-agent/internal/workflow"
)

// drainConn unmarshals everything buffered on the connection's send channel.
func drainConn(t *testing.T, conn *wsConn) []map[string]any {
	t.Helper()
	var out []map[string]any
	for len(conn.send) > 0 {
		var ev map[string]any
		if err := json.Unmarshal(<-conn.send, &ev); err != nil {
			t.Fatalf("unmarshal replayed event: %v", err)
		}
		out = append(out, ev)
	}
	return out
}

// seedLiveTurn installs a live state as doAgentTurn does at turn start.
func seedLiveTurn(srv *Server, sid string) {
	srv.liveStateMu.Lock()
	srv.liveStates[sid] = &sessionLiveState{
		progress: &wsEventProgress{
			Type:         "progress",
			ProgressType: "thinking",
			Phase:        "active",
			StartedAt:    1,
		},
	}
	srv.liveStateMu.Unlock()
}

// TestReplayLiveState_ReplaysBufferedTurnEvents is the regression guard for
// the "refresh mid-turn loses every tool card" bug: tool_call / tool_result
// events and streamed text only reach the tabs connected when they were
// broadcast, and the session file doesn't gain the turn's messages until the
// turn ends — so a reloaded page showed nothing but a progress spinner until
// the agent finished.
func TestReplayLiveState_ReplaysBufferedTurnEvents(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()
	srv.pendingQuestions = map[string]wsEventRequestUserQuestion{}
	srv.pendingConfirms = map[string]wsEventRequestConfirmation{}

	const sid = "replay-turn-session"
	defer tools.CloseSessionBackgroundManager(sid)

	seedLiveTurn(srv, sid)
	sw := srv.newWSStreamWriter(sid)

	// Round 1: thinking + text, then a tool call that succeeds.
	sw.handleEvent(agent.AgentEvent{Kind: agent.EventThinkingDelta, Text: "hmm, "})
	sw.handleEvent(agent.AgentEvent{Kind: agent.EventThinkingDelta, Text: "files first"})
	sw.handleEvent(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "Let me "})
	sw.handleEvent(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "check."})
	sw.handleEvent(agent.AgentEvent{
		Kind: agent.EventToolStarted, ToolName: "terminal",
		Input: map[string]any{"command": "ls"},
	})
	sw.handleEvent(agent.AgentEvent{
		Kind: agent.EventToolDone, Output: "file.txt",
		UI: map[string]any{"kind": "terminal"},
	})

	// A steer message lands between rounds.
	sw.handleEvent(agent.AgentEvent{Kind: agent.EventSteerInjected, Messages: []string{"also check go.mod"}})

	// Round 2: a tool call that errors, then in-flight streaming text.
	sw.handleEvent(agent.AgentEvent{
		Kind: agent.EventToolStarted, ToolName: "read_file",
		Input: map[string]any{"path": "go.mod"},
	})
	sw.handleEvent(agent.AgentEvent{Kind: agent.EventToolError, Err: "no such file"})
	sw.handleEvent(agent.AgentEvent{Kind: agent.EventTextDelta, Text: "Half a reply"})

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.replayLiveState(sid, conn)

	var got []string
	var progressSeen bool
	for _, ev := range drainConn(t, conn) {
		switch ev["type"] {
		case "thinking_delta", "text_delta":
			got = append(got, fmt.Sprintf("%s:%s", ev["type"], ev["text"]))
		case "tool_call":
			got = append(got, fmt.Sprintf("tool_call:%s", ev["name"]))
		case "tool_result":
			if ev["ui_payload"] == nil {
				t.Errorf("replayed tool_result lost its ui_payload: %v", ev)
			}
			got = append(got, fmt.Sprintf("tool_result:%s", ev["result"]))
		case "tool_error":
			got = append(got, fmt.Sprintf("tool_error:%s", ev["error"]))
		case "history_user_message":
			got = append(got, fmt.Sprintf("steer:%s", ev["content"]))
		case "progress":
			progressSeen = true
		}
	}

	want := []string{
		"thinking_delta:hmm, files first",
		"text_delta:Let me check.",
		"tool_call:terminal",
		"tool_result:file.txt",
		"steer:also check go.mod",
		"tool_call:read_file",
		"tool_error:no such file",
		"text_delta:Half a reply",
	}
	if len(got) != len(want) {
		t.Fatalf("replayed transcript = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("replayed[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if !progressSeen {
		t.Error("replay did not include the active progress indicator")
	}
}

// TestReplayLiveState_NothingAfterTurnPersists checks that once the live
// state is dropped (doAgentTurn after Save), a subscribing tab gets no
// buffered transcript events — history is the source from then on, and
// replaying the buffer too would render every tool card twice.
func TestReplayLiveState_NothingAfterTurnPersists(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()
	srv.pendingQuestions = map[string]wsEventRequestUserQuestion{}
	srv.pendingConfirms = map[string]wsEventRequestConfirmation{}

	const sid = "replay-done-session"
	defer tools.CloseSessionBackgroundManager(sid)

	seedLiveTurn(srv, sid)
	sw := srv.newWSStreamWriter(sid)
	sw.handleEvent(agent.AgentEvent{
		Kind: agent.EventToolStarted, ToolName: "terminal",
		Input: map[string]any{"command": "ls"},
	})

	// Simulate the post-Save cleanup in doAgentTurn.
	srv.liveStateMu.Lock()
	delete(srv.liveStates, sid)
	srv.liveStateMu.Unlock()

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.replayLiveState(sid, conn)

	for _, ev := range drainConn(t, conn) {
		switch ev["type"] {
		case "tool_call", "tool_result", "text_delta", "thinking_delta", "progress":
			t.Errorf("event %v replayed after live state was dropped", ev["type"])
		}
	}
}

// TestReplayLiveState_IdleSessionSendsIdleUpdate checks that a subscribing tab
// receives an explicit idle snapshot when the session has no live turn. Without
// this, a tab that switched away while the turn ran and missed the completion
// broadcast would keep showing a stale thinking indicator after switching back.
func TestReplayLiveState_IdleSessionSendsIdleUpdate(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()
	srv.pendingQuestions = map[string]wsEventRequestUserQuestion{}
	srv.pendingConfirms = map[string]wsEventRequestConfirmation{}

	const sid = "replay-idle-session"
	defer tools.CloseSessionBackgroundManager(sid)

	// No live state means the session is idle.
	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.replayLiveState(sid, conn)

	var idleSeen bool
	for _, ev := range drainConn(t, conn) {
		if ev["type"] == "session_update" && ev["status"] == "idle" && ev["session_id"] == sid {
			idleSeen = true
		}
		if ev["type"] == "progress" || ev["type"] == "text_delta" || ev["type"] == "thinking_delta" {
			t.Errorf("idle session replayed a live-turn event: %v", ev["type"])
		}
	}
	if !idleSeen {
		t.Error("idle session replay did not include a session_update{status:idle} snapshot")
	}
}

// TestReplayLiveState_IdleWithPendingPrompt sends both the idle snapshot and
// any outstanding interactive prompt when there is no live turn. The two must
// not be mutually exclusive — a tab refreshing at the exact moment a question
// is waiting should still see the idle reset and the question modal.
func TestReplayLiveState_IdleWithPendingPrompt(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()
	srv.pendingQuestions = map[string]wsEventRequestUserQuestion{}
	srv.pendingConfirms = map[string]wsEventRequestConfirmation{}

	const sid = "replay-idle-prompt-session"
	defer tools.CloseSessionBackgroundManager(sid)

	srv.pendingQuestions[sid] = wsEventRequestUserQuestion{
		Type: "request_user_question", SessionID: sid, QuestionID: "q_1", Question: "pick one", Options: []string{"a", "b"},
	}

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.replayLiveState(sid, conn)

	var idleSeen, questionSeen bool
	for _, ev := range drainConn(t, conn) {
		switch ev["type"] {
		case "session_update":
			if ev["status"] == "idle" && ev["session_id"] == sid {
				idleSeen = true
			}
		case "request_user_question":
			questionSeen = true
		}
	}
	if !idleSeen {
		t.Error("idle session with pending prompt did not replay idle snapshot")
	}
	if !questionSeen {
		t.Error("idle session with pending prompt did not replay the question")
	}
}

// TestSessionLiveState_EventBufferCap checks the replay buffer drops its
// oldest entries instead of growing without bound on very long turns.
func TestSessionLiveState_EventBufferCap(t *testing.T) {
	ls := &sessionLiveState{}
	for i := 0; i < maxLiveTurnEvents+50; i++ {
		ls.appendEvent(map[string]any{"type": "tool_call", "seq": i})
	}
	if len(ls.events) != maxLiveTurnEvents {
		t.Fatalf("buffer length = %d, want %d", len(ls.events), maxLiveTurnEvents)
	}
	if first := ls.events[0]["seq"]; first != 50 {
		t.Errorf("oldest kept event seq = %v, want 50", first)
	}
}

// EventToolProgress must reach subscribed tabs immediately as tool_stdout
// (issue #1094) — before this, the agent loop never even called
// ExecuteStream (see DefaultRegistry.ExecuteStream), so this event never
// fired in production regardless of what handleEvent did with it.
func TestHandleEvent_ToolProgress_BroadcastsToolStdout(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()

	const sid = "progress-broadcast-session"
	defer tools.CloseSessionBackgroundManager(sid)
	seedLiveTurn(srv, sid)
	sw := srv.newWSStreamWriter(sid)

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.subscribe(conn, sid)

	sw.handleEvent(agent.AgentEvent{
		Kind: agent.EventToolStarted, ToolName: "terminal", ToolID: "t1",
		Input: map[string]any{"command": "make test"},
	})
	sw.handleEvent(agent.AgentEvent{Kind: agent.EventToolProgress, ToolID: "t1", Chunk: "compiling..."})
	sw.handleEvent(agent.AgentEvent{Kind: agent.EventToolProgress, ToolID: "t1", Chunk: "ok"})

	// hub.broadcast is delivered by the hub's async dispatch goroutine, not
	// inline in handleEvent — wait for it instead of draining immediately.
	waitFor(t, func() bool { return len(conn.send) >= 3 }) // tool_call + 2 tool_stdout

	var stdoutEvents []map[string]any
	for _, ev := range drainConn(t, conn) {
		if ev["type"] == "tool_stdout" {
			stdoutEvents = append(stdoutEvents, ev)
		}
	}
	if len(stdoutEvents) != 2 {
		t.Fatalf("got %d tool_stdout broadcasts, want 2", len(stdoutEvents))
	}
	if stdoutEvents[0]["tool_id"] != "t1" {
		t.Errorf("tool_stdout missing tool_id, got %v", stdoutEvents[0])
	}
	if lines, ok := stdoutEvents[1]["lines"].([]any); !ok || len(lines) != 1 || lines[0] != "ok" {
		t.Errorf("second tool_stdout lines = %v, want [\"ok\"]", stdoutEvents[1]["lines"])
	}
}

// A late-subscribing tab (page refresh mid-command) must catch up on the
// running tool's output so far via replayLiveState.
func TestReplayLiveState_IncludesLiveStdout(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()
	srv.pendingQuestions = map[string]wsEventRequestUserQuestion{}
	srv.pendingConfirms = map[string]wsEventRequestConfirmation{}

	const sid = "replay-stdout-session"
	defer tools.CloseSessionBackgroundManager(sid)
	seedLiveTurn(srv, sid)
	sw := srv.newWSStreamWriter(sid)

	sw.handleEvent(agent.AgentEvent{
		Kind: agent.EventToolStarted, ToolName: "terminal", ToolID: "t1",
		Input: map[string]any{"command": "make test"},
	})
	sw.handleEvent(agent.AgentEvent{Kind: agent.EventToolProgress, ToolID: "t1", Chunk: "line one"})
	sw.handleEvent(agent.AgentEvent{Kind: agent.EventToolProgress, ToolID: "t1", Chunk: "line two"})

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.replayLiveState(sid, conn)

	var found bool
	for _, ev := range drainConn(t, conn) {
		if ev["type"] != "tool_stdout" {
			continue
		}
		found = true
		lines, ok := ev["lines"].([]any)
		if !ok || len(lines) != 2 || lines[0] != "line one" || lines[1] != "line two" {
			t.Errorf("replayed tool_stdout lines = %v", ev["lines"])
		}
		if ev["tool_id"] != "t1" {
			t.Errorf("replayed tool_stdout tool_id = %v, want t1", ev["tool_id"])
		}
	}
	if !found {
		t.Fatal("replay did not include the in-flight tool's stdout")
	}
}

// A finished tool's stdout must not bleed into the next round's replay: once
// reseedThinkingProgress fires (tool done/errored), a tab subscribing before
// the next tool call starts should see no leftover stdout.
func TestReplayLiveState_StdoutClearsAfterToolDone(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()
	srv.pendingQuestions = map[string]wsEventRequestUserQuestion{}
	srv.pendingConfirms = map[string]wsEventRequestConfirmation{}

	const sid = "replay-stdout-cleared-session"
	defer tools.CloseSessionBackgroundManager(sid)
	seedLiveTurn(srv, sid)
	sw := srv.newWSStreamWriter(sid)

	sw.handleEvent(agent.AgentEvent{
		Kind: agent.EventToolStarted, ToolName: "terminal", ToolID: "t1",
		Input: map[string]any{"command": "make test"},
	})
	sw.handleEvent(agent.AgentEvent{Kind: agent.EventToolProgress, ToolID: "t1", Chunk: "line one"})
	sw.handleEvent(agent.AgentEvent{Kind: agent.EventToolDone, ToolID: "t1", Output: "done"})

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.replayLiveState(sid, conn)

	for _, ev := range drainConn(t, conn) {
		if ev["type"] == "tool_stdout" {
			t.Errorf("stale tool_stdout replayed after the tool finished: %v", ev)
		}
	}
}

// TestReplayLiveState_ReplaysTaskPanel is the regression guard for the task
// panel vanishing on refresh. The store is per-session (not per-turn) and
// outlives the turn, and replayLiveState rebuilds the panel from it
// unconditionally — so the panel survives a refresh even AFTER the turn ends,
// with no live turn to snapshot.
func TestReplayLiveState_ReplaysTaskPanel(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()
	srv.pendingQuestions = map[string]wsEventRequestUserQuestion{}
	srv.pendingConfirms = map[string]wsEventRequestConfirmation{}

	const sid = "replay-task-panel-session"
	defer tools.CloseSessionBackgroundManager(sid)
	defer tools.CloseSessionTaskStore(sid)

	// Seed the per-session store as the task_* tools would, then DON'T seed a
	// live turn — this is the post-turn refresh case the per-turn store lost.
	store := tools.SessionTaskStore(sid)
	if _, err := store.Create("step one", "", ""); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := store.Create("step two", "", ""); err != nil {
		t.Fatalf("create task: %v", err)
	}

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.replayLiveState(sid, conn)

	var got []map[string]any
	for _, ev := range drainConn(t, conn) {
		if ev["type"] == "todo_update" && ev["session_id"] == sid {
			got = append(got, ev)
		}
	}
	if len(got) != 1 {
		t.Fatalf("replayed todo_update count = %d, want 1", len(got))
	}
	if list, ok := got[0]["todos"].([]any); !ok || len(list) != 2 {
		t.Fatalf("replayed todos = %v, want 2 items", got[0]["todos"])
	}
}

// TestReplayLiveState_SkipsCompletedTaskPanel guards that a fully-completed plan
// is NOT replayed: it fades out client-side once done, so a refresh afterwards
// must not bring the panel back (and the store is cleared at the next turn).
func TestReplayLiveState_SkipsCompletedTaskPanel(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()
	srv.pendingQuestions = map[string]wsEventRequestUserQuestion{}
	srv.pendingConfirms = map[string]wsEventRequestConfirmation{}

	const sid = "replay-completed-task-session"
	defer tools.CloseSessionBackgroundManager(sid)
	defer tools.CloseSessionTaskStore(sid)

	store := tools.SessionTaskStore(sid)
	id, _ := store.Create("only step", "", "")
	done := tasks.Completed
	if _, err := store.Update(id, tasks.UpdateField{Status: &done}); err != nil {
		t.Fatalf("update: %v", err)
	}

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.replayLiveState(sid, conn)

	for _, ev := range drainConn(t, conn) {
		if ev["type"] == "todo_update" {
			t.Errorf("replayed a todo_update for a fully-completed plan: %v", ev)
		}
	}
}

// TestReplayLiveState_NoTaskPanelWhenEmpty guards the leak/no-op side: a session
// that never ran a task tool must not create a store or replay a todo_update.
func TestReplayLiveState_NoTaskPanelWhenEmpty(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()
	srv.pendingQuestions = map[string]wsEventRequestUserQuestion{}
	srv.pendingConfirms = map[string]wsEventRequestConfirmation{}

	const sid = "replay-no-task-session"
	defer tools.CloseSessionBackgroundManager(sid)

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.replayLiveState(sid, conn)

	for _, ev := range drainConn(t, conn) {
		if ev["type"] == "todo_update" {
			t.Errorf("replayed a todo_update for a session with no tasks: %v", ev)
		}
	}
	if tools.PeekSessionTaskStore(sid) != nil {
		t.Error("replay created a task store for a session that never used one")
	}
}

// TestReplayLiveState_ReplaysAllRunningSyncSubAgents is the regression guard
// for a parallel sub_agent fan-out losing all but one entry on a mid-turn
// refresh: every concurrently-running foreground (sync) sub-agent must replay
// its own started event, not just one.
func TestReplayLiveState_ReplaysAllRunningSyncSubAgents(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()

	const sid = "replay-fanout-session"
	defer tools.CloseSessionSubAgentManager(sid)

	block := make(chan struct{})
	defer close(block)
	mgr := tools.SessionSubAgentManager(sid, func() tools.Spawner { return &blockingSpawner{block: block} })

	const n = 4
	for i := 0; i < n; i++ {
		i := i
		go func() {
			_, _ = mgr.RunSync(context.Background(), tools.SpawnRequest{
				Description: fmt.Sprintf("fanout-%d", i),
			})
		}()
	}

	// Wait until all n are registered and running.
	waitFor(t, func() bool { return len(mgr.ListRunning()) == n })

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.replayLiveState(sid, conn)

	started := map[string]bool{}
	for _, ev := range drainConn(t, conn) {
		if ev["type"] == "sub_agent_event" && ev["kind"] == "started" {
			started[fmt.Sprintf("%v", ev["agent_id"])] = true
		}
	}
	if len(started) != n {
		t.Fatalf("replayed started events for %d sub-agents, want %d", len(started), n)
	}
}

// TestReplayLiveState_ReplaysRunningWorkflow is the regression guard for the
// web workflow panel never appearing (or never clearing) when a tab
// (re)subscribes after a background workflow already started: the panel is
// built entirely from workflow_event pushes with no initial fetch, and
// broadcast() is fire-and-forget, so a tab that wasn't connected when
// "started"/"progress" fired used to never learn the run exists.
func TestReplayLiveState_ReplaysRunningWorkflow(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()

	const sid = "replay-workflow-session"
	defer tools.CloseSessionWorkflowManager(sid)

	block := make(chan struct{})
	blockingAgent := func(ctx context.Context, _ string, _ workflow.AgentOptions) workflow.AgentResult {
		<-block
		return workflow.AgentResult{Reply: "done"}
	}
	mgr := tools.SessionWorkflowManager(sid)
	id, err := mgr.Start(tools.WorkflowRunRequest{
		Description: "test workflow",
		Script:      `log("step one"); agent("x")`,
		Agent:       blockingAgent,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer close(block)

	// Wait for the script to emit its progress line and reach the blocking
	// agent call, so the run is still "running" when we replay. Generous
	// deadline: under `go test -race` the mruby interpreter runs several
	// times slower (see waitForDone in workflow_manager_test.go).
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if snap, ok := mgr.Read(id); ok && len(snap.Logs) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.replayLiveState(sid, conn)

	var sawStarted bool
	var progressLines []any
	for _, ev := range drainConn(t, conn) {
		if ev["type"] != "workflow_event" || ev["run_id"] != id {
			continue
		}
		switch ev["kind"] {
		case "started":
			sawStarted = true
		case "progress":
			progressLines = append(progressLines, ev["line"])
		}
	}
	if !sawStarted {
		t.Error("replay did not include the workflow's started event")
	}
	found := false
	for _, l := range progressLines {
		if l == "step one" {
			found = true
		}
	}
	if !found {
		t.Errorf("replay progress lines = %v, want one of them to be %q", progressLines, "step one")
	}
}

// blockingSpawner's Spawn hangs until unblocked, so a Start()ed sub-agent
// stays "running" long enough for a test to replay it.
type blockingSpawner struct{ block <-chan struct{} }

func (s *blockingSpawner) Spawn(ctx context.Context, _ tools.SpawnRequest) (tools.SpawnResult, error) {
	select {
	case <-s.block:
	case <-ctx.Done():
	}
	return tools.SpawnResult{Reply: "done"}, nil
}

func (s *blockingSpawner) Continue(ctx context.Context, _, _ string) (tools.SpawnResult, error) {
	return tools.SpawnResult{}, nil
}

// toolEmittingBlockingSpawner blocks like blockingSpawner, but emits a single
// tool-level sub-agent event before blocking so the replay can be tested.
type toolEmittingBlockingSpawner struct{ block <-chan struct{} }

func (s *toolEmittingBlockingSpawner) Spawn(ctx context.Context, req tools.SpawnRequest) (tools.SpawnResult, error) {
	if sink := tools.SubAgentEventSink(ctx); sink != nil {
		sink(tools.SubAgentEvent{Kind: "tool", ToolName: "read_file", ToolInput: map[string]any{"path": "go.mod"}})
	}
	select {
	case <-s.block:
	case <-ctx.Done():
	}
	return tools.SpawnResult{Reply: "done"}, nil
}

func (s *toolEmittingBlockingSpawner) Continue(ctx context.Context, _, _ string) (tools.SpawnResult, error) {
	return tools.SpawnResult{}, nil
}

// TestReplayLiveState_ReplaysRunningSubAgent is the regression guard for the
// same gap in the sub-agent panel: SubAgentOnEvent also broadcasts directly
// with no buffering, so a tab that (re)subscribes after a sub-agent already
// started never learned it existed.
func TestReplayLiveState_ReplaysRunningSubAgent(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()

	const sid = "replay-subagent-session"
	defer tools.CloseSessionSubAgentManager(sid)

	block := make(chan struct{})
	defer close(block)
	mgr := tools.SessionSubAgentManager(sid, func() tools.Spawner { return &blockingSpawner{block: block} })
	id, err := mgr.Start(tools.SpawnRequest{Description: "test sub-agent", Prompt: "do it"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.replayLiveState(sid, conn)

	var sawStarted bool
	for _, ev := range drainConn(t, conn) {
		if ev["type"] == "sub_agent_event" && ev["agent_id"] == id && ev["kind"] == "started" {
			sawStarted = true
		}
	}
	if !sawStarted {
		t.Error("replay did not include the sub-agent's started event")
	}
}

// TestReplayLiveState_ReplaysSubAgentToolHistory checks that switching back
// to a session with a running sub-agent restores the tool trail, not just a
// coarse "started" stub.
func TestReplayLiveState_ReplaysSubAgentToolHistory(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()

	const sid = "replay-subagent-tool-history-session"
	defer tools.CloseSessionSubAgentManager(sid)

	block := make(chan struct{})
	defer close(block)
	mgr := tools.SessionSubAgentManager(sid, func() tools.Spawner { return &toolEmittingBlockingSpawner{block: block} })

	id, err := mgr.Start(tools.SpawnRequest{
		Description: "test sub-agent",
		Prompt:      "do it",
		AgentType:   "explore",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the goroutine to emit the tool event and enter the block.
	waitFor(t, func() bool {
		infos := mgr.ListRunning()
		for _, in := range infos {
			if in.ID == id {
				for _, ev := range in.Events {
					if ev.Kind == "tool" {
						return true
					}
				}
			}
		}
		return false
	})

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.replayLiveState(sid, conn)

	var sawStarted, sawTool bool
	for _, ev := range drainConn(t, conn) {
		if ev["type"] != "sub_agent_event" || ev["agent_id"] != id {
			continue
		}
		switch ev["kind"] {
		case "started":
			if ev["agent_type"] != "explore" {
				t.Errorf("replayed started agent_type = %v, want explore", ev["agent_type"])
			}
			sawStarted = true
		case "tool":
			if ev["tool_name"] != "read_file" {
				t.Errorf("replayed tool tool_name = %v, want read_file", ev["tool_name"])
			}
			input, ok := ev["tool_input"].(map[string]any)
			if !ok || input["path"] != "go.mod" {
				t.Errorf("replayed tool tool_input = %v, want {path: go.mod}", ev["tool_input"])
			}
			sawTool = true
		case "done":
			t.Error("replay should not include done events for running agents")
		}
	}
	if !sawStarted {
		t.Error("replay did not include the sub-agent's started event")
	}
	if !sawTool {
		t.Error("replay did not include the sub-agent's tool event")
	}
}

// gatedSpawner blocks the "busy" spawn until released; every other spawn
// completes immediately. It lets a test hold one sub-agent genuinely running
// while another finishes and goes idle.
type gatedSpawner struct{ release chan struct{} }

func (g *gatedSpawner) Spawn(ctx context.Context, req tools.SpawnRequest) (tools.SpawnResult, error) {
	if req.Description == "busy-one" {
		select {
		case <-g.release:
			return tools.SpawnResult{Reply: "busy done"}, nil
		case <-ctx.Done():
			return tools.SpawnResult{}, ctx.Err()
		}
	}
	return tools.SpawnResult{Reply: "idle done"}, nil
}

func (g *gatedSpawner) Continue(context.Context, string, string) (tools.SpawnResult, error) {
	return tools.SpawnResult{}, nil
}

// TestReplayLiveState_SkipsIdleSubAgents: a completed-but-retained async
// sub-agent (idle, kept so sub_agent_send can resume it) must NOT be replayed
// to a subscribing tab — the retained events rebuild it as "running" on the
// live panel, a ghost that only the next turn start clears. Only genuinely
// busy agents have a live state worth replaying.
func TestReplayLiveState_SkipsIdleSubAgents(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()

	const sid = "replay-subagent-ghost"
	spawner := &gatedSpawner{release: make(chan struct{})}
	defer close(spawner.release)
	mgr := tools.SessionSubAgentManager(sid, func() tools.Spawner { return spawner })
	t.Cleanup(func() { tools.CloseSessionSubAgentManager(sid) })

	idleID, err := mgr.Start(tools.SpawnRequest{Description: "idle-one"})
	if err != nil {
		t.Fatalf("start idle: %v", err)
	}
	busyID, err := mgr.Start(tools.SpawnRequest{Description: "busy-one"})
	if err != nil {
		t.Fatalf("start busy: %v", err)
	}

	// Wait for the idle agent to finish its round (busy=false, retained).
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, status, found := mgr.Read(idleID)
		if found && status == "idle" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("idle agent never settled (status %q)", status)
		}
		time.Sleep(5 * time.Millisecond)
	}

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.replayLiveState(sid, conn)

	var idleReplayed, busyReplayed bool
	for _, ev := range drainConn(t, conn) {
		if ev["type"] != "sub_agent_event" {
			continue
		}
		switch ev["agent_id"] {
		case idleID:
			idleReplayed = true
		case busyID:
			busyReplayed = true
		}
	}
	if idleReplayed {
		t.Error("idle (completed) sub-agent must not be replayed — it rebuilds a ghost 'running' panel entry")
	}
	if !busyReplayed {
		t.Error("a genuinely running sub-agent must still be replayed")
	}
}
