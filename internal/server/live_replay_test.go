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
