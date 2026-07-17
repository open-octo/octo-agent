package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestDoAgentTurn_Interrupt_KeepsUserMessage_NoReload: interrupting a turn
// before its first round completes must KEEP the unanswered user message
// (capped with the interrupt note) and must NOT broadcast history_reload —
// the old rollback+reload combination cleared the rendered transcript and
// re-fetched a history that no longer contained the message the user just
// sent, blanking the chat (fully blank on a fresh session).
func TestDoAgentTurn_Interrupt_KeepsUserMessage_NoReload(t *testing.T) {
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
	// Prior history + the interrupted user message + the assistant interrupt
	// note: the input survives the interrupt instead of rolling back.
	if len(reloaded.Messages) != wantLen+2 {
		t.Fatalf("persisted message count = %d, want %d (user message + interrupt note kept)", len(reloaded.Messages), wantLen+2)
	}
	if m := reloaded.Messages[wantLen]; m.Role != agent.RoleUser || m.Content != "second question" {
		t.Errorf("messages[%d] = %+v, want the interrupted user message", wantLen, m)
	}
	if m := reloaded.Messages[wantLen+1]; m.Role != agent.RoleAssistant {
		t.Errorf("messages[%d].Role = %q, want assistant interrupt note", wantLen+1, m.Role)
	}

	// doAgentTurn has returned, so every broadcast is already queued: a single
	// drain sees them all. History did not shrink, so no reload may fire.
	for _, ev := range drainConn(t, conn) {
		if ev["type"] == "history_reload" {
			t.Fatal("history_reload broadcast on interrupt — this is the blank-transcript bug")
		}
	}
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

// blockUntilCanceledSender blocks its first provider call until the context is
// canceled — an in-flight streaming turn awaiting an interrupt. Later calls
// (an edit's rerun) reply normally.
type blockUntilCanceledSender struct {
	entered chan struct{}
	calls   atomic.Int32
}

func (s *blockUntilCanceledSender) SendMessages(ctx context.Context, _, _ string, _ []agent.Message, _ int) (agent.Reply, error) {
	if s.calls.Add(1) == 1 {
		close(s.entered)
		<-ctx.Done()
		return agent.Reply{}, ctx.Err()
	}
	return agent.Reply{Content: "edited reply"}, nil
}

func (s *blockUntilCanceledSender) StreamMessages(ctx context.Context, _, _ string, _ []agent.Message, _ int, _ func(string), _ func(string)) (agent.Reply, error) {
	return s.SendMessages(ctx, "", "", nil, 0)
}

// TestHandleEditMessage_MidStream_InterruptsAndReruns: editing the prompt of a
// turn that is still streaming must interrupt that turn, wait out its
// wind-down, tolerate the first-round rollback having already popped the
// prompt (message_index == len), and rerun with the edited content — which
// lands in history exactly once, with a fresh reply.
func TestHandleEditMessage_MidStream_InterruptsAndReruns(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	sender := &blockUntilCanceledSender{entered: make(chan struct{})}
	srv.sender = sender
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	sess.Title = "fixed title"
	sess.Messages = []agent.Message{
		agent.NewUserMessage("one"),
		{Role: agent.RoleAssistant, Content: "two"},
	}
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Start a turn the way handleWSUserMessage does: flag it running, run it
	// on a goroutine, clear the flag when it fully winds down.
	mu := srv.sessionTurnLock(sess.ID)
	mu.Lock()
	srv.turnRunning[sess.ID] = true
	mu.Unlock()
	turnDone := make(chan struct{})
	go func() {
		defer func() {
			mu.Lock()
			srv.turnRunning[sess.ID] = false
			mu.Unlock()
			close(turnDone)
		}()
		srv.doAgentTurn(sess, "original prompt", nil, nil)
	}()
	<-sender.entered // the turn is now mid-stream, blocked in the provider

	// The frontend holds message_index 2 — the live prediction for the
	// in-flight prompt. Edit it while the turn is still streaming.
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sess.ID+"/edit_message",
		strings.NewReader(`{"message_index":2,"new_content":"EDITED"}`))
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("mid-stream edit: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	<-turnDone

	var reloaded *agent.Session
	waitFor(t, func() bool {
		var err error
		reloaded, err = agent.LoadSession(sess.ID)
		return err == nil && len(reloaded.Messages) == 4 &&
			reloaded.Messages[3].Role == agent.RoleAssistant
	})
	// The edited prompt replaced the original exactly once, no reminder prefix
	// (the rollback left history ending on an assistant message), and the
	// rerun produced a fresh reply.
	if reloaded.Messages[2].Content != "EDITED" {
		t.Fatalf("prompt = %q, want EDITED", reloaded.Messages[2].Content)
	}
	if reloaded.Messages[3].Content != "edited reply" {
		t.Fatalf("reply = %q, want edited reply", reloaded.Messages[3].Content)
	}
	for _, m := range reloaded.Messages {
		if m.Content == "original prompt" {
			t.Fatal("the original in-flight prompt must not survive the edit")
		}
	}
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
