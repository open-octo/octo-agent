package server

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
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
