package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/scheduler"
)

// replayOrder drains a connection's replayed messages and reports, in
// arrival order, the type of each plus the first index (if any) at which
// history_user_message and tool_call appeared and the former's content —
// shared by the doAgentTurn and RunTask variants of the reconnect-ordering
// regression below.
func replayOrder(t *testing.T, conn *wsConn) (types []string, userMsgIdx, toolCallIdx int, userMsgContent string) {
	t.Helper()
	userMsgIdx, toolCallIdx = -1, -1
	for len(conn.send) > 0 {
		b := <-conn.send
		var ev map[string]any
		if err := json.Unmarshal(b, &ev); err != nil {
			t.Fatalf("unmarshal replayed event: %v", err)
		}
		typ, _ := ev["type"].(string)
		types = append(types, typ)
		switch typ {
		case "history_user_message":
			if userMsgIdx < 0 {
				userMsgIdx = len(types) - 1
				userMsgContent, _ = ev["content"].(string)
			}
		case "tool_call":
			if toolCallIdx < 0 {
				toolCallIdx = len(types) - 1
			}
		}
	}
	return types, userMsgIdx, toolCallIdx, userMsgContent
}

// TestReplayLiveState_HistoryUserMessagePrecedesToolCall guards a real report:
// a brand-new session's first message is broadcast (doAgentTurn) BEFORE the
// session's live-state replay buffer even exists, so it was never buffered
// there — unlike the tool_call/tool_result events the turn goes on to emit,
// which ARE buffered (EventToolStarted/EventToolDone). A tab that (re)connects
// after missing that first broadcast — a real scenario on a flaky mobile
// connection — replayed the tool card with no user bubble above it, since
// nothing else in the live-state path could supply it (the REST history
// endpoint deliberately excludes the in-flight turn's own messages; see
// TestDoAgentTurn_PersistsProgressIncrementally's watermark check). The fix
// buffers the user's own message into the same replay list, in order, ahead
// of the tool_call.
func TestReplayLiveState_HistoryUserMessagePrecedesToolCall(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sender := &blockingToolSender{
		entered1: make(chan struct{}),
		release1: make(chan struct{}),
		entered2: make(chan struct{}),
		release2: make(chan struct{}),
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: true})
	srv.sender = sender
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]queuedTurn)
	srv.sessionAgents = make(map[string]*agent.Agent)
	srv.pendingQuestions = map[string]wsEventRequestUserQuestion{}
	srv.pendingConfirms = map[string]wsEventRequestConfirmation{}

	sess := agent.NewSession("stub-model", "")
	sess.Title = "fixed title" // suppress the async title-generation side-call
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// The connection that sent the message and is live for the whole turn —
	// stands in for a client that (unlike the reconnecting one below) never
	// missed the original broadcast.
	live := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.register <- live
	srv.wsHub.subscribe(live, sess.ID)

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.doAgentTurn(sess, "hello reconnect", nil, nil)
	}()
	releaseOnce := func(ch chan struct{}) {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
	t.Cleanup(func() {
		releaseOnce(sender.release1)
		releaseOnce(sender.release2)
		<-done
	})

	select {
	case <-sender.entered1:
	case <-time.After(5 * time.Second):
		t.Fatal("round 1 never started")
	}
	releaseOnce(sender.release1)

	select {
	case <-sender.entered2:
	case <-time.After(5 * time.Second):
		t.Fatal("round 2 never started")
	}

	// ── Mid-turn: round 2 is blocked inside the provider call, tool_call for
	// round 1's read_file has already fired and been buffered. ──
	//
	// Simulate a tab that reconnects now, having missed the original
	// history_user_message broadcast entirely (it fired before this
	// connection existed) — the exact scenario a dropped/reconnecting WS hits.
	reconnecting := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.replayLiveState(sess.ID, reconnecting)

	types, userMsgIdx, toolCallIdx, userMsgContent := replayOrder(t, reconnecting)

	if userMsgIdx < 0 {
		t.Fatalf("reconnecting tab's replay never included history_user_message; got %v", types)
	}
	if userMsgContent != "hello reconnect" {
		t.Errorf("replayed history_user_message content = %q, want %q", userMsgContent, "hello reconnect")
	}
	if toolCallIdx < 0 {
		t.Fatalf("reconnecting tab's replay never included the round-1 tool_call; got %v", types)
	}
	if userMsgIdx > toolCallIdx {
		t.Errorf("history_user_message replayed at %d, tool_call at %d — user message must precede the tool card it precedes in reality; got %v",
			userMsgIdx, toolCallIdx, types)
	}

	releaseOnce(sender.release2)
	<-done
}

// TestReplayLiveState_RunTask_HistoryUserMessagePrecedesToolCall is the
// RunTask (scheduled/cron turn) mirror of the test above: RunTask has the
// identical broadcast-before-live-state-exists gap, fixed the same way, so it
// gets the same regression coverage rather than relying on doAgentTurn's test
// to stand in for both call sites.
func TestReplayLiveState_RunTask_HistoryUserMessagePrecedesToolCall(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sender := &blockingToolSender{
		entered1: make(chan struct{}),
		release1: make(chan struct{}),
		entered2: make(chan struct{}),
		release2: make(chan struct{}),
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: true})
	srv.sender = sender
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]queuedTurn)
	srv.sessionAgents = make(map[string]*agent.Agent)
	srv.pendingQuestions = map[string]wsEventRequestUserQuestion{}
	srv.pendingConfirms = map[string]wsEventRequestConfirmation{}

	sessionID, err := srv.CreateSession(scheduler.Task{Name: "t"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess, err := agent.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	sess.Title = "fixed title" // suppress the async title-generation side-call
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	live := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.register <- live
	srv.wsHub.subscribe(live, sessionID)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = srv.RunTask(context.Background(), scheduler.Task{Name: "t", Prompt: "hello scheduled reconnect", SessionID: sessionID})
	}()
	releaseOnce := func(ch chan struct{}) {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
	t.Cleanup(func() {
		releaseOnce(sender.release1)
		releaseOnce(sender.release2)
		<-done
	})

	select {
	case <-sender.entered1:
	case <-time.After(5 * time.Second):
		t.Fatal("round 1 never started")
	}
	releaseOnce(sender.release1)

	select {
	case <-sender.entered2:
	case <-time.After(5 * time.Second):
		t.Fatal("round 2 never started")
	}

	reconnecting := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.replayLiveState(sessionID, reconnecting)

	types, userMsgIdx, toolCallIdx, userMsgContent := replayOrder(t, reconnecting)

	if userMsgIdx < 0 {
		t.Fatalf("reconnecting tab's replay never included history_user_message; got %v", types)
	}
	if userMsgContent != "hello scheduled reconnect" {
		t.Errorf("replayed history_user_message content = %q, want %q", userMsgContent, "hello scheduled reconnect")
	}
	if toolCallIdx < 0 {
		t.Fatalf("reconnecting tab's replay never included the round-1 tool_call; got %v", types)
	}
	if userMsgIdx > toolCallIdx {
		t.Errorf("history_user_message replayed at %d, tool_call at %d — user message must precede the tool card it precedes in reality; got %v",
			userMsgIdx, toolCallIdx, types)
	}

	releaseOnce(sender.release2)
	<-done
}
