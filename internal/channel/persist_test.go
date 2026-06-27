package channel

import (
	"os"
	"strings"
	"testing"
	"time"

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

// TestCmdUnbind_KeepsStore: /unbind detaches the chat but must not delete the
// session's persisted history — it can be re-attached later with /bind.
func TestCmdUnbind_KeepsStore(t *testing.T) {
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

	if reply := m.cmdUnbind(ev); !strings.Contains(strings.ToLower(reply), "unbound") &&
		!strings.Contains(strings.ToLower(reply), "wasn't bound") {
		t.Fatalf("unexpected unbind reply %q", reply)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("/unbind must keep the persisted history, but the file is gone: %v", err)
	}
}

// TestCmdBind_AttachesToExistingSession: /bind <id> redirects a chat to an
// existing session and rehydrates its history (it does not start fresh).
func TestCmdBind_AttachesToExistingSession(t *testing.T) {
	tempHome(t)
	m := testManager()

	// Chat A builds up a session and persists it.
	evA := InboundEvent{Platform: "feishu", ChatID: "cA", UserID: "uA"}
	sessA := m.GetOrCreateSession(evA)
	sessA.Agent.History.Append(agent.Message{Role: agent.RoleUser, Content: "shared context"})
	if err := sessA.Persist(); err != nil {
		t.Fatal(err)
	}
	targetID := sessA.Store.ID

	// Chat B binds to A's session by ID and should see A's history.
	evB := InboundEvent{Platform: "feishu", ChatID: "cB", UserID: "uB"}
	if reply := m.cmdBind(evB, []string{targetID}); !strings.Contains(strings.ToLower(reply), "bound") {
		t.Fatalf("unexpected bind reply %q", reply)
	}
	bound := m.GetSession(evB)
	if bound == nil {
		t.Fatal("expected a session after /bind")
	}
	if got := len(bound.Agent.History.Snapshot()); got != 1 {
		t.Errorf("history after /bind = %d messages, want 1 (rehydrated from target)", got)
	}
	if bound.Store.ID != targetID {
		t.Errorf("bound session store = %q, want %q", bound.Store.ID, targetID)
	}
	if !bound.Store.BoundTo(agent.EntryChannel) {
		t.Errorf("bound session entry = %q, want %q", bound.Store.BoundEntry, agent.EntryChannel)
	}

	// The binding survives a rebuild (persisted): a fresh manager re-attaches.
	m2 := testManager()
	again := m2.GetOrCreateSession(evB)
	if again.Store.ID != targetID {
		t.Errorf("binding did not persist: store = %q, want %q", again.Store.ID, targetID)
	}
}

// TestCmdBind_RejectedWhenBoundToOtherEntry: /bind must not silently take over
// a session owned by web/tui/cli.
func TestCmdBind_RejectedWhenBoundToOtherEntry(t *testing.T) {
	tempHome(t)
	m := testManager()

	// A session owned by the web entry.
	webSess := agent.NewSession("stub-model", "")
	webSess.BoundEntry = agent.EntryWeb
	webSess.BoundAt = time.Now()
	webSess.Messages = []agent.Message{{Role: agent.RoleUser, Content: "web context"}}
	if err := webSess.Save(); err != nil {
		t.Fatal(err)
	}

	evB := InboundEvent{Platform: "feishu", ChatID: "cB", UserID: "uB"}
	reply := m.cmdBind(evB, []string{webSess.ID})
	if !strings.Contains(strings.ToLower(reply), "cannot bind") {
		t.Fatalf("expected rejection, got %q", reply)
	}
	if m.GetSession(evB) != nil {
		t.Fatal("expected no session after rejected /bind")
	}
}

// TestCmdBind_ForceTakesOverOtherEntry: /bind --force may take over a session
// owned by another entry, as long as no turn lease is active.
func TestCmdBind_ForceTakesOverOtherEntry(t *testing.T) {
	tempHome(t)
	m := testManager()

	// A session owned by the web entry, with no active lease.
	webSess := agent.NewSession("stub-model", "")
	webSess.BoundEntry = agent.EntryWeb
	webSess.BoundAt = time.Now()
	webSess.Messages = []agent.Message{{Role: agent.RoleUser, Content: "web context"}}
	if err := webSess.Save(); err != nil {
		t.Fatal(err)
	}

	evB := InboundEvent{Platform: "feishu", ChatID: "cB", UserID: "uB"}
	reply := m.cmdBind(evB, []string{"--force", webSess.ID})
	if !strings.Contains(strings.ToLower(reply), "taken over") {
		t.Fatalf("expected takeover success, got %q", reply)
	}
	if !strings.Contains(reply, agent.EntryWeb) {
		t.Fatalf("expected reply to name previous owner %q, got %q", agent.EntryWeb, reply)
	}

	bound := m.GetSession(evB)
	if bound == nil {
		t.Fatal("expected a session after /bind --force")
	}
	if !bound.Store.BoundTo(agent.EntryChannel) {
		t.Errorf("bound session entry = %q, want %q", bound.Store.BoundEntry, agent.EntryChannel)
	}

	// The binding is persisted: a fresh manager sees channel ownership.
	m2 := testManager()
	again := m2.GetOrCreateSession(evB)
	if !again.Store.BoundTo(agent.EntryChannel) {
		t.Errorf("persisted entry after takeover = %q, want %q", again.Store.BoundEntry, agent.EntryChannel)
	}
}

// TestCmdBind_ForceRejectedWhenLeaseActive: /bind --force cannot steal a
// session while another entry holds an active turn lease.
func TestCmdBind_ForceRejectedWhenLeaseActive(t *testing.T) {
	tempHome(t)
	m := testManager()

	webSess := agent.NewSession("stub-model", "")
	webSess.BoundEntry = agent.EntryWeb
	webSess.BoundAt = time.Now()
	if err := webSess.Save(); err != nil {
		t.Fatal(err)
	}
	if err := webSess.WriteLease(agent.EntryWeb, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	evB := InboundEvent{Platform: "feishu", ChatID: "cB", UserID: "uB"}
	reply := m.cmdBind(evB, []string{webSess.ID, "--force"})
	if !strings.Contains(strings.ToLower(reply), "cannot bind") {
		t.Fatalf("expected rejection due to active lease, got %q", reply)
	}
	if m.GetSession(evB) != nil {
		t.Fatal("expected no session after rejected forced /bind")
	}
}

// TestCmdUnbind_ReleasesBoundEntry: /unbind clears the IM entry binding so
// other entries can use the session.
func TestCmdUnbind_ReleasesBoundEntry(t *testing.T) {
	tempHome(t)
	ev := InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1"}

	m := testManager()
	sess := m.GetOrCreateSession(ev)
	sess.Agent.History.Append(agent.Message{Role: agent.RoleUser, Content: "secret"})
	if err := sess.Persist(); err != nil {
		t.Fatal(err)
	}

	m.cmdUnbind(ev)

	reloaded, err := agent.LoadSession(sess.Store.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.BoundEntry != "" {
		t.Errorf("BoundEntry = %q after unbind, want empty", reloaded.BoundEntry)
	}
}

// TestCmdClear_WipesHistoryKeepsStore: /clear empties the conversation but
// keeps the session and its (now-empty) store file.
func TestCmdClear_WipesHistoryKeepsStore(t *testing.T) {
	tempHome(t)
	ev := InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1"}

	m := testManager()
	sess := m.GetOrCreateSession(ev)
	sess.Agent.History.Append(agent.Message{Role: agent.RoleUser, Content: "remember me"})
	if err := sess.Persist(); err != nil {
		t.Fatal(err)
	}
	path, err := sess.Store.SavePath()
	if err != nil {
		t.Fatal(err)
	}
	storeID := sess.Store.ID

	if reply := m.cmdClear(ev); !strings.Contains(strings.ToLower(reply), "cleared") {
		t.Fatalf("unexpected clear reply %q", reply)
	}
	if got := len(sess.Agent.History.Snapshot()); got != 0 {
		t.Errorf("in-memory history after /clear = %d, want 0", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("/clear must keep the store file: %v", err)
	}
	reloaded, err := agent.LoadSession(storeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Messages) != 0 {
		t.Errorf("persisted history after /clear = %d, want 0", len(reloaded.Messages))
	}
}

func TestParseBindArgs(t *testing.T) {
	cases := []struct {
		args       []string
		wantSteal  bool
		wantTarget string
		wantOK     bool
	}{
		{args: []string{"abc"}, wantSteal: false, wantTarget: "abc", wantOK: true},
		{args: []string{"--force", "abc"}, wantSteal: true, wantTarget: "abc", wantOK: true},
		{args: []string{"abc", "--force"}, wantSteal: true, wantTarget: "abc", wantOK: true},
		{args: []string{"--Force", "abc"}, wantSteal: true, wantTarget: "abc", wantOK: true},
		{args: []string{}, wantOK: false},
		{args: []string{"--force"}, wantOK: false},
		{args: []string{"abc", "def"}, wantOK: false},
		{args: []string{"abc", "--force", "def"}, wantOK: false},
	}
	for _, c := range cases {
		steal, target, ok := parseBindArgs(c.args)
		if steal != c.wantSteal || target != c.wantTarget || ok != c.wantOK {
			t.Errorf("parseBindArgs(%v) = (%v, %q, %v), want (%v, %q, %v)",
				c.args, steal, target, ok, c.wantSteal, c.wantTarget, c.wantOK)
		}
	}
}

// TestDeleteStore_TombstonesAgainstZombiePersist pins the clear-during-turn
// contract: after deleteStore tombstones the store, a turn that finishes later
// must not recreate the file via its post-turn Persist.
func TestDeleteStore_TombstonesAgainstZombiePersist(t *testing.T) {
	tempHome(t)
	ev := InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1"}

	m := testManager()
	sess := m.GetOrCreateSession(ev)
	sess.Agent.History.Append(agent.Message{Role: agent.RoleUser, Content: "private"})
	if err := sess.Persist(); err != nil {
		t.Fatal(err)
	}
	path, err := sess.Store.SavePath()
	if err != nil {
		t.Fatal(err)
	}

	if err := sess.deleteStore(); err != nil {
		t.Fatalf("deleteStore: %v", err)
	}

	// The zombie turn finishes now and persists — the tombstone must hold.
	sess.Agent.History.Append(agent.Message{Role: agent.RoleAssistant, Content: "late reply"})
	if err := sess.Persist(); err != nil {
		t.Fatalf("zombie Persist should no-op, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("zombie turn resurrected the deleted history file")
	}
}
