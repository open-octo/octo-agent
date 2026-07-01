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
