package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/scheduler"
)

// erroringSender fails every provider call so a turn errors on its first round
// — the path where runLoop/turnStream rolls the just-appended user message back
// out of history.
type erroringSender struct{}

func (erroringSender) SendMessages(context.Context, string, string, []agent.Message, int) (agent.Reply, error) {
	return agent.Reply{}, errors.New("provider boom")
}

func (erroringSender) StreamMessages(_ context.Context, _, _ string, _ []agent.Message, _ int, _ func(string), _ func(string)) (agent.Reply, error) {
	return agent.Reply{}, errors.New("provider boom")
}

// TestDoAgentTurn_ErrorRollback_BroadcastsHistoryReload: when a turn fails on
// its first LLM round the loop rolls the user message back out of history and
// the post-turn save erases the crash-safety copy persisted before the turn.
// The browser is still showing that user bubble with a now out-of-range
// message_index (the "message_index out of range: N (have N)" edit/branch bug),
// so the server must broadcast a history_reload to realign it with disk.
func TestDoAgentTurn_ErrorRollback_BroadcastsHistoryReload(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.sender = erroringSender{}
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	sess.Title = "fixed title"
	sess.Messages = []agent.Message{
		agent.NewUserMessage("first question"),
		{Role: agent.RoleAssistant, Content: "first answer"},
	}
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	wantLen := len(sess.Messages)

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.subscribe(conn, sess.ID)

	srv.doAgentTurn(sess, "second question", nil, nil)

	// The errored turn rolled the user message back out — disk is unchanged, so
	// index wantLen (which the live broadcast handed the browser) no longer
	// exists.
	reloaded, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.Messages) != wantLen {
		t.Fatalf("persisted message count = %d, want %d (user message should have rolled back)", len(reloaded.Messages), wantLen)
	}

	// The browser must be told to re-fetch so the stale bubble/index goes away.
	var seen []map[string]any
	waitFor(t, func() bool {
		seen = append(seen, drainConn(t, conn)...)
		for _, ev := range seen {
			if ev["type"] == "history_reload" {
				return true
			}
		}
		return false
	})
}

// interruptingSender simulates the user hitting Stop right after sending:
// the first provider call interrupts its own session, then honors the
// cancellation. The unanswered user message gets rolled back out of history
// exactly like a first-round provider error.
type interruptingSender struct {
	srv *Server
	sid string
}

func (s *interruptingSender) SendMessages(ctx context.Context, _, _ string, _ []agent.Message, _ int) (agent.Reply, error) {
	s.srv.interruptSession(s.sid)
	<-ctx.Done()
	return agent.Reply{}, ctx.Err()
}

func (s *interruptingSender) StreamMessages(ctx context.Context, _, _ string, _ []agent.Message, _ int, _ func(string), _ func(string)) (agent.Reply, error) {
	s.srv.interruptSession(s.sid)
	<-ctx.Done()
	return agent.Reply{}, ctx.Err()
}

// TestDoAgentTurn_InterruptRollback_BroadcastsHistoryReload: interrupting a
// turn before its first round completes rolls the unanswered user message back
// just like a provider error does — the reload must fire on the canceled path
// too, not only the error-toast path ("send → immediately Stop → Edit" is the
// most natural way to hit the out-of-range 400).
func TestDoAgentTurn_InterruptRollback_BroadcastsHistoryReload(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	sess.Title = "fixed title"
	sess.Messages = []agent.Message{
		agent.NewUserMessage("first question"),
		{Role: agent.RoleAssistant, Content: "first answer"},
	}
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	wantLen := len(sess.Messages)
	srv.sender = &interruptingSender{srv: srv, sid: sess.ID}

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.subscribe(conn, sess.ID)

	srv.doAgentTurn(sess, "second question", nil, nil)

	reloaded, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.Messages) != wantLen {
		t.Fatalf("persisted message count = %d, want %d (interrupted user message should have rolled back)", len(reloaded.Messages), wantLen)
	}

	var seen []map[string]any
	waitFor(t, func() bool {
		seen = append(seen, drainConn(t, conn)...)
		for _, ev := range seen {
			if ev["type"] == "history_reload" {
				return true
			}
		}
		return false
	})
}

// failSecondRoundSender drives one successful tool round then errors, so the
// turn fails at iteration 1 — past the first-round rollback point. The user
// message and the completed round stay in history.
type failSecondRoundSender struct{ round atomic.Int32 }

func (s *failSecondRoundSender) SendMessages(context.Context, string, string, []agent.Message, int) (agent.Reply, error) {
	return agent.Reply{Content: "side-call"}, nil
}

func (s *failSecondRoundSender) StreamMessages(_ context.Context, _, _ string, _ []agent.Message, _ int, _ func(string), _ func(string)) (agent.Reply, error) {
	return agent.Reply{Content: "side-call"}, nil
}

func (s *failSecondRoundSender) SendMessagesWithTools(context.Context, string, string, []agent.Message, int, []agent.ToolDefinition) (agent.Reply, error) {
	return agent.Reply{Content: "side-call"}, nil
}

func (s *failSecondRoundSender) StreamMessagesWithTools(_ context.Context, _, _ string, _ []agent.Message, _ int, _ []agent.ToolDefinition, _ func(string), _ agent.ToolInputDeltaFunc, _ agent.ThinkingDeltaFunc) (agent.Reply, error) {
	if s.round.Add(1) == 1 {
		// A read of a nonexistent path: allowed by the embedded permission
		// defaults and fails fast with an error tool_result, keeping the loop
		// running into round 2 with no filesystem side effects.
		return agent.Reply{
			Blocks: []agent.ContentBlock{
				agent.NewToolUseBlock("tu1", "read_file", map[string]any{"path": "/nonexistent/edit-branch-index-test"}),
			},
			StopReason: "tool_use",
		}, nil
	}
	return agent.Reply{}, errors.New("provider boom")
}

// TestDoAgentTurn_MidTurnError_NoHistoryReload is the other half of the
// reload gate: a failure AFTER the first round keeps the user message (and the
// completed round) in history, so no reload must fire — the browser's indices
// are still valid and a reload would needlessly redraw the transcript.
func TestDoAgentTurn_MidTurnError_NoHistoryReload(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: true})
	srv.sender = &failSecondRoundSender{}
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	sess.Title = "fixed title"
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.subscribe(conn, sess.ID)

	srv.doAgentTurn(sess, "run the tool", nil, nil)

	reloaded, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.Messages) == 0 {
		t.Fatal("mid-turn failure must keep the user message and completed round on disk")
	}

	// complete is the turn's final broadcast — once it arrives, any reload
	// would already have been delivered before it (hub dispatch is FIFO).
	var seen []map[string]any
	waitFor(t, func() bool {
		seen = append(seen, drainConn(t, conn)...)
		for _, ev := range seen {
			if ev["type"] == "complete" {
				return true
			}
		}
		return false
	})
	for _, ev := range seen {
		if ev["type"] == "history_reload" {
			t.Fatal("history did not shrink — no history_reload should have been broadcast")
		}
	}
}

// TestRunTask_UserMessageBroadcastCarriesIndex: the scheduled-task turn path
// broadcasts the task prompt as a history_user_message — it must carry the
// persisted message_index like doAgentTurn's, or an edit/branch on that bubble
// sends index 0 and clobbers the session's first message.
func TestRunTask_UserMessageBroadcastCarriesIndex(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	// Pre-create the task session and seed prior turns so the expected index
	// is nonzero — distinguishing a real value from undefined-defaulting-to-0.
	sessionID, err := srv.CreateSession(scheduler.Task{Name: "t"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess, err := agent.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	sess.Title = "fixed title"
	sess.Messages = []agent.Message{
		agent.NewUserMessage("earlier run"),
		{Role: agent.RoleAssistant, Content: "earlier reply"},
	}
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.subscribe(conn, sessionID)

	if _, err := srv.RunTask(context.Background(), scheduler.Task{Name: "t", Prompt: "do the thing", SessionID: sessionID}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	var userEv map[string]any
	waitFor(t, func() bool {
		for _, ev := range drainConn(t, conn) {
			if ev["type"] == "history_user_message" {
				userEv = ev
			}
		}
		return userEv != nil
	})
	if idx, ok := userEv["message_index"].(float64); !ok || int(idx) != 2 {
		t.Errorf("task prompt message_index = %v, want 2", userEv["message_index"])
	}
}

// TestRunTask_ErrorRollback_BroadcastsHistoryReload: RunTask's error path has
// the same rollback-erases-persisted-prompt contract as doAgentTurn and must
// tell watching tabs to re-fetch.
func TestRunTask_ErrorRollback_BroadcastsHistoryReload(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.sender = erroringSender{}
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sessionID, err := srv.CreateSession(scheduler.Task{Name: "t"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess, err := agent.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	sess.Title = "fixed title"
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.subscribe(conn, sessionID)

	if _, err := srv.RunTask(context.Background(), scheduler.Task{Name: "t", Prompt: "will fail", SessionID: sessionID}); err == nil {
		t.Fatal("RunTask should surface the provider error")
	}

	var seen []map[string]any
	waitFor(t, func() bool {
		seen = append(seen, drainConn(t, conn)...)
		for _, ev := range seen {
			if ev["type"] == "history_reload" {
				return true
			}
		}
		return false
	})
}

// TestHandleEvent_SteerInjected_CarriesMessageIndex: a steer message injected
// mid-turn must reach the browser with its persisted message_index so edit and
// branch can target it. Reminder-only items are skipped for display but still
// occupy a history slot, so the surviving items keep their true index
// (SteerBaseIndex + loop position), not a compacted counter.
func TestHandleEvent_SteerInjected_CarriesMessageIndex(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()

	const sid = "steer-index-session"
	seedLiveTurn(srv, sid)
	sw := srv.newWSStreamWriter(sid)

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.subscribe(conn, sid)

	// Item 1 (k=1) is empty and skipped for display, but still occupies history
	// slot 6 — so "world" at k=2 must be labelled 7, not 6.
	sw.handleEvent(agent.AgentEvent{
		Kind:           agent.EventSteerInjected,
		SteerBaseIndex: 5,
		Steer: []agent.InboxItem{
			{Text: "hello"},
			{Text: ""},
			{Text: "world"},
		},
	})

	var msgs []map[string]any
	waitFor(t, func() bool {
		for _, ev := range drainConn(t, conn) {
			if ev["type"] == "history_user_message" {
				msgs = append(msgs, ev)
			}
		}
		return len(msgs) >= 2
	})

	if len(msgs) != 2 {
		t.Fatalf("got %d history_user_message broadcasts, want 2", len(msgs))
	}
	if idx, _ := msgs[0]["message_index"].(float64); int(idx) != 5 {
		t.Errorf("first steer message_index = %v, want 5", msgs[0]["message_index"])
	}
	if idx, _ := msgs[1]["message_index"].(float64); int(idx) != 7 {
		t.Errorf("second steer message_index = %v, want 7 (skipped item still occupies slot 6)", msgs[1]["message_index"])
	}
}
