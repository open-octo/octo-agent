package server

import (
	"context"
	"errors"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
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
