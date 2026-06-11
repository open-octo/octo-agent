package channel

import (
	"os"
	"strings"
	"testing"

	"github.com/Leihb/octo-agent/internal/agent"
)

func tempHome(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
}

func testManager() *Manager {
	return NewManager(&Config{}, func() *agent.Agent {
		return agent.New(nil, "stub-model")
	}, BindByChatUser)
}

func TestSessionStoreID_DeterministicAndSafe(t *testing.T) {
	a := sessionStoreID(SessionKey("feishu:oc_4a/b#c:ou_x"))
	b := sessionStoreID(SessionKey("feishu:oc_4a/b#c:ou_x"))
	if a != b {
		t.Errorf("same key produced different IDs: %q vs %q", a, b)
	}
	if c := sessionStoreID(SessionKey("feishu:oc_4a/b#c:ou_y")); c == a {
		t.Error("different keys must produce different IDs")
	}
	for _, r := range a {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.", r) {
			t.Fatalf("ID %q contains filename-unsafe rune %q", a, r)
		}
	}
}

func TestSession_PersistAndRestore(t *testing.T) {
	tempHome(t)
	ev := InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1", Text: "hi"}

	m1 := testManager()
	sess := m1.GetOrCreateSession(ev)
	sess.Agent.History.Append(agent.Message{Role: agent.RoleUser, Content: "remember the number 42"})
	sess.Agent.History.Append(agent.Message{Role: agent.RoleAssistant, Content: "noted: 42"})
	if err := sess.Persist(); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	// A fresh manager simulates the post-restart process: same event key must
	// come back with the conversation history.
	m2 := testManager()
	restored := m2.GetOrCreateSession(ev)
	msgs := restored.Agent.History.Snapshot()
	if len(msgs) != 2 {
		t.Fatalf("restored history has %d messages, want 2", len(msgs))
	}
	if msgs[1].Content != "noted: 42" {
		t.Errorf("restored msg = %q, want %q", msgs[1].Content, "noted: 42")
	}
	if restored.Store.Source != "channel" {
		t.Errorf("store source = %q, want channel", restored.Store.Source)
	}
}

func TestSession_PersistAcrossTurnsAppends(t *testing.T) {
	tempHome(t)
	ev := InboundEvent{Platform: "tg", ChatID: "c", UserID: "u"}

	m := testManager()
	sess := m.GetOrCreateSession(ev)
	sess.Agent.History.Append(agent.Message{Role: agent.RoleUser, Content: "one"})
	if err := sess.Persist(); err != nil {
		t.Fatal(err)
	}
	sess.Agent.History.Append(agent.Message{Role: agent.RoleAssistant, Content: "two"})
	if err := sess.Persist(); err != nil {
		t.Fatal(err)
	}

	restored := testManager().GetOrCreateSession(ev)
	if got := len(restored.Agent.History.Snapshot()); got != 2 {
		t.Errorf("history after two persists = %d messages, want 2", got)
	}
}

func TestCmdUnbind_DeletesStore(t *testing.T) {
	tempHome(t)
	ev := InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1"}

	m := testManager()
	sess := m.GetOrCreateSession(ev)
	sess.Agent.History.Append(agent.Message{Role: agent.RoleUser, Content: "secret"})
	if err := sess.Persist(); err != nil {
		t.Fatal(err)
	}
	path, err := sess.Store.SavePath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("store file missing before unbind: %v", err)
	}

	if reply := m.cmdUnbind(ev); !strings.Contains(reply, "unbound") {
		t.Fatalf("unexpected unbind reply %q", reply)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("/unbind must delete the persisted history")
	}
}

// TestCmdBind_StartsFresh: /bind explicitly resets the conversation; the new
// session must not rehydrate the previous history from disk.
func TestCmdBind_StartsFresh(t *testing.T) {
	tempHome(t)
	ev := InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1"}

	m := testManager()
	sess := m.GetOrCreateSession(ev)
	sess.Agent.History.Append(agent.Message{Role: agent.RoleUser, Content: "old context"})
	if err := sess.Persist(); err != nil {
		t.Fatal(err)
	}

	if reply := m.cmdBind(ev, nil); !strings.Contains(reply, "bound") {
		t.Fatalf("unexpected bind reply %q", reply)
	}
	fresh := m.GetSession(ev)
	if got := len(fresh.Agent.History.Snapshot()); got != 0 {
		t.Errorf("history after /bind = %d messages, want 0 (fresh session)", got)
	}
}
