package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/tools"
)

// TestReplayLiveState_ReplaysPendingPrompts is the regression guard for the
// "refresh during ask_user_question" bug: the question's original broadcast
// only reached the tabs connected at the time, so a reloaded page showed a
// dead spinner with no way to answer.
func TestReplayLiveState_ReplaysPendingPrompts(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()
	srv.pendingQuestions = map[string]wsEventRequestUserQuestion{}
	srv.pendingConfirms = map[string]wsEventRequestConfirmation{}

	const sid = "replay-prompt-session"
	defer tools.CloseSessionBackgroundManager(sid)

	srv.pendingQuestions[sid] = wsEventRequestUserQuestion{
		Type: "request_user_question", SessionID: sid, QuestionID: "q_1", Question: "pick one", Options: []string{"a", "b"},
	}
	srv.pendingConfirms[sid] = wsEventRequestConfirmation{
		Type: "request_confirmation", SessionID: sid, ConfID: "conf_1", Message: "Allow terminal?", Kind: "yes_no",
	}

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.replayLiveState(sid, conn)

	var gotQ, gotC bool
	for len(conn.send) > 0 {
		var ev map[string]any
		if json.Unmarshal(<-conn.send, &ev) != nil {
			continue
		}
		switch ev["type"] {
		case "request_user_question":
			gotQ = true
			if ev["question_id"] != "q_1" {
				t.Errorf("question_id = %v, want q_1", ev["question_id"])
			}
			if ev["session_id"] == nil || ev["session_id"] == "" {
				t.Error("replayed question must carry session_id (the dispatcher filters on it)")
			}
		case "request_confirmation":
			gotC = true
			// `id` + `session_id` are what the dispatcher reads — the old
			// conf_id/session-less shape made the frontend drop the event.
			if ev["id"] != "conf_1" {
				t.Errorf("id = %v, want conf_1", ev["id"])
			}
			if ev["session_id"] == nil || ev["session_id"] == "" {
				t.Error("replayed confirmation must carry session_id")
			}
		}
	}
	if !gotQ || !gotC {
		t.Fatalf("replayed question=%v confirmation=%v, want both", gotQ, gotC)
	}
}

// TestWSAsker_RegistersAndClearsPendingQuestion drives a full ask round-trip
// and checks the pending entry exists while the ask is outstanding and is
// gone after the answer arrives.
func TestWSAsker_RegistersAndClearsPendingQuestion(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()
	srv.questionChans = map[string]chan tools.AskResponse{}
	srv.pendingQuestions = map[string]wsEventRequestUserQuestion{}
	srv.pendingConfirms = map[string]wsEventRequestConfirmation{}

	const sid = "asker-pending-session"
	ctx := context.WithValue(context.Background(), ctxKeySessionID{}, sid)

	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			srv.pendingPromptMu.Lock()
			ev, ok := srv.pendingQuestions[sid]
			srv.pendingPromptMu.Unlock()
			if ok {
				srv.handleWSUserQuestionAnswer(ev.QuestionID, []string{"a"}, "", false)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	res, err := srv.wsAsker().Ask(ctx, tools.AskRequest{Question: "pick", Options: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(res.Choices) != 1 || res.Choices[0] != "a" {
		t.Fatalf("choices = %v, want [a]", res.Choices)
	}

	srv.pendingPromptMu.Lock()
	_, still := srv.pendingQuestions[sid]
	srv.pendingPromptMu.Unlock()
	if still {
		t.Fatal("pending question not cleared after answer")
	}
}

// TestAcquireAskSlot_Serializes is the regression guard for the concurrent-
// prompt clobber: two askers for the same session must not both hold the slot,
// so a parallel workflow agent / background task can't overwrite an in-flight
// prompt's pending slot and frontend modal (orphaning the first asker until its
// timeout — a hang).
func TestAcquireAskSlot_Serializes(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	const sid = "serialize-session"

	rel1, err := srv.acquireAskSlot(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}

	acquired := make(chan struct{})
	go func() {
		rel2, err := srv.acquireAskSlot(context.Background(), sid)
		if err == nil {
			rel2()
		}
		close(acquired)
	}()

	select {
	case <-acquired:
		t.Fatal("second acquire returned while the slot was still held")
	case <-time.After(50 * time.Millisecond):
	}

	rel1()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second acquire never proceeded after release")
	}
}

// Serialization is per-session: a different session has an independent slot.
func TestAcquireAskSlot_PerSession(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	rel1, err := srv.acquireAskSlot(context.Background(), "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	defer rel1()

	rel2, err := srv.acquireAskSlot(context.Background(), "sess-b")
	if err != nil {
		t.Fatalf("a different session must not block on a held slot: %v", err)
	}
	rel2()
}

// A cancelled ctx returns an error instead of waiting behind a held slot, so a
// cancelled turn doesn't block until the prompt's own timeout.
func TestAcquireAskSlot_CtxCancel(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	const sid = "cancel-session"

	rel1, err := srv.acquireAskSlot(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	defer rel1()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := srv.acquireAskSlot(ctx, sid); err == nil {
		t.Fatal("cancelled ctx should not acquire a held slot")
	}
}

// drainForEvent polls conn.send (non-blocking) until an event matching pred
// arrives, skipping unrelated events instead of failing on the first
// mismatch — broadcasts unrelated to the assertion (e.g. other session_update
// pings) may interleave.
func drainForEvent(t *testing.T, conn *wsConn, pred func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case b := <-conn.send:
			var ev map[string]any
			if json.Unmarshal(b, &ev) != nil {
				continue
			}
			if pred(ev) {
				return ev
			}
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	t.Fatal("timed out waiting for matching event")
	return nil
}

// waitForPendingQuestionID polls until sid has a registered pending question
// and returns its question_id, so tests can answer it without racing wsAsker
// registering it after Ask() starts running in its own goroutine.
func waitForPendingQuestionID(t *testing.T, srv *Server, sid string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		srv.pendingPromptMu.Lock()
		q, ok := srv.pendingQuestions[sid]
		srv.pendingPromptMu.Unlock()
		if ok {
			return q.QuestionID
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("pending question never registered")
	return ""
}

// TestWSAsker_SessionActivityReachesUnsubscribedConn is the regression guard
// for the sidebar badge / desktop notification (item 3/6): a tab that never
// subscribed to the asking session has no other way to learn a question
// opened or resolved there — request_user_question / dismiss_user_question
// are session-subscriber-only broadcasts, so session_activity must be a
// global one reaching every connection regardless of subscription.
func TestWSAsker_SessionActivityReachesUnsubscribedConn(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()
	srv.questionChans = map[string]chan tools.AskResponse{}
	srv.pendingQuestions = map[string]wsEventRequestUserQuestion{}
	srv.pendingConfirms = map[string]wsEventRequestConfirmation{}

	const sid = "activity-session"
	ctx := context.WithValue(context.Background(), ctxKeySessionID{}, sid)

	// Registered but never subscribed to sid — only a global broadcast can
	// reach it.
	other := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.register <- other

	done := make(chan tools.AskResponse, 1)
	go func() {
		res, _ := srv.wsAsker().Ask(ctx, tools.AskRequest{Question: "pick", Options: []string{"a", "b"}})
		done <- res
	}()

	ev := drainForEvent(t, other, func(ev map[string]any) bool {
		return ev["type"] == "session_activity" && ev["kind"] == "question_pending"
	})
	if ev["session_id"] != sid {
		t.Fatalf("session_id = %v, want %v", ev["session_id"], sid)
	}

	qid := waitForPendingQuestionID(t, srv, sid)
	srv.handleWSUserQuestionAnswer(qid, []string{"a"}, "", false)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Ask never returned after answer")
	}

	drainForEvent(t, other, func(ev map[string]any) bool {
		return ev["type"] == "session_activity" && ev["kind"] == "question_resolved"
	})
}

// TestWSAsker_SuccessDismissesOtherSubscribedTabs is the regression guard for
// the multi-tab dead-modal bug (item 2): when one tab answers, every other
// tab subscribed to the same session must get dismiss_user_question too —
// previously that only fired on cancellation/timeout, so a tab that didn't
// answer kept showing a modal that silently no-ops if submitted.
func TestWSAsker_SuccessDismissesOtherSubscribedTabs(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()
	srv.questionChans = map[string]chan tools.AskResponse{}
	srv.pendingQuestions = map[string]wsEventRequestUserQuestion{}
	srv.pendingConfirms = map[string]wsEventRequestConfirmation{}

	const sid = "dismiss-session"
	ctx := context.WithValue(context.Background(), ctxKeySessionID{}, sid)

	watcher := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.register <- watcher
	srv.wsHub.subscribe(watcher, sid)

	done := make(chan tools.AskResponse, 1)
	go func() {
		res, _ := srv.wsAsker().Ask(ctx, tools.AskRequest{Question: "pick", Options: []string{"a", "b"}})
		done <- res
	}()

	drainForEvent(t, watcher, func(ev map[string]any) bool { return ev["type"] == "request_user_question" })

	qid := waitForPendingQuestionID(t, srv, sid)
	// A different tab answers — watcher never does.
	srv.handleWSUserQuestionAnswer(qid, []string{"a"}, "", false)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Ask never returned after answer")
	}

	drainForEvent(t, watcher, func(ev map[string]any) bool { return ev["type"] == "dismiss_user_question" })
}
