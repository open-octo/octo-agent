package server

import (
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

// A web session gets the interface note on its first turn only: the second
// turn's history already carries it. Exercises the real wiring — buildAgent
// registers the note before it assigns the agent's history, so the note must
// read the history when it fires.
func TestDoAgentTurn_InterfaceNoteOnlyWhenChanged(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sender := &msgRecordingSender{}
	srv := newCrashReminderServer(t, sender)

	sess := agent.NewSession("stub-model", "")
	sess.Title = "fixed title" // no title call on the sender
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	srv.doAgentTurn(sess, "first", nil, nil)
	again, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	srv.doAgentTurn(again, "second", nil, nil)

	sender.mu.Lock()
	calls := sender.calls
	sender.mu.Unlock()
	// Find each turn's call by its user text; other calls (a title, say) may
	// share the sender.
	turn := func(text string) string {
		for _, msgs := range calls {
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i].Role != agent.RoleUser {
					continue
				}
				if strings.TrimSpace(agent.StripSystemReminders(msgs[i].Content)) == text {
					return msgs[i].Content
				}
				break
			}
		}
		t.Fatalf("no provider call for the %q turn", text)
		return ""
	}
	if got := turn("first"); !strings.Contains(got, "Interface: the Web UI.") {
		t.Errorf("first turn = %q, want the interface note", got)
	}
	if got := turn("second"); strings.Contains(got, "Interface:") {
		t.Errorf("second turn = %q, want no repeat of the note", got)
	}
}
