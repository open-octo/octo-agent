package server

import (
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

// findTurnError returns the first turn_error event in seen, or nil.
func findTurnError(seen []map[string]any) map[string]any {
	for _, ev := range seen {
		if ev["type"] == "turn_error" {
			return ev
		}
	}
	return nil
}

// TestDoAgentTurn_FirstRoundError_TurnErrorMarksInputRolledBack: a failure on
// the first LLM round rolls the user message back out of history, so the text
// the user typed survives nowhere — the composer cleared it optimistically and
// the history_reload wipes the bubble. turn_error must say so, or the browser
// has no way to tell this apart from a mid-turn failure and the prompt is lost.
func TestDoAgentTurn_FirstRoundError_TurnErrorMarksInputRolledBack(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.sender = erroringSender{}
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]queuedTurn)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	sess.Title = "fixed title"
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.subscribe(conn, sess.ID)

	srv.doAgentTurn(sess, "a long prompt the user typed", nil, nil)

	var seen []map[string]any
	waitFor(t, func() bool {
		seen = append(seen, drainConn(t, conn)...)
		return findTurnError(seen) != nil
	})
	ev := findTurnError(seen)
	if rolled, _ := ev["input_rolled_back"].(bool); !rolled {
		t.Errorf("turn_error = %+v, want input_rolled_back true — the typed text is unrecoverable without it", ev)
	}
}

// TestDoAgentTurn_MidTurnError_TurnErrorKeepsInput is the other half of the
// gate: a failure after the first round keeps the user message in history and
// in the transcript, so the flag must stay off — restoring the text there would
// duplicate the message on the next send.
func TestDoAgentTurn_MidTurnError_TurnErrorKeepsInput(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: true})
	srv.sender = &failSecondRoundSender{}
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]queuedTurn)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	sess.Title = "fixed title"
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.subscribe(conn, sess.ID)

	srv.doAgentTurn(sess, "run the tool", nil, nil)

	var seen []map[string]any
	waitFor(t, func() bool {
		seen = append(seen, drainConn(t, conn)...)
		return findTurnError(seen) != nil
	})
	ev := findTurnError(seen)
	if _, present := ev["input_rolled_back"]; present {
		t.Errorf("turn_error = %+v, want no input_rolled_back — the user message survived the turn", ev)
	}
}
