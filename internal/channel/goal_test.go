package channel

import (
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

func TestCmdGoal_AppliesSharedGrammarOnStore(t *testing.T) {
	tempHome(t)
	ev := InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1"}

	mgr := NewManager(&Config{}, fakeAgentFactory, BindByChatUser)

	// No session yet.
	if r := mgr.cmdGoal(ev, "anything"); !strings.Contains(r, "No active session") {
		t.Errorf("no-session reply = %q", r)
	}

	sess := mgr.GetOrCreateSession(ev)
	if r := mgr.cmdGoal(ev, "ship the release"); !strings.Contains(r, "Goal set") {
		t.Errorf("create reply = %q", r)
	}

	// The goal lives on the persisted backing store, visible to any
	// transport that loads the same session.
	store := sess.GoalStore()
	if store == nil {
		t.Fatal("backing store missing")
	}
	g, ok := store.GoalSnapshot()
	if !ok || g.Objective != "ship the release" || g.Status != agent.GoalActive {
		t.Fatalf("goal on store = %+v", g)
	}
	reloaded, err := agent.LoadSession(store.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rg, ok := reloaded.GoalSnapshot(); !ok || rg.Objective != "ship the release" {
		t.Errorf("goal must persist to disk: %+v", rg)
	}

	if r := mgr.cmdGoal(ev, "pause"); !strings.Contains(r, "paused") {
		t.Errorf("pause reply = %q", r)
	}

	// A tombstoned store (concurrent /unbind) degrades gracefully.
	_ = sess.deleteStore()
	if r := mgr.cmdGoal(ev, ""); !strings.Contains(r, "unavailable") {
		t.Errorf("tombstoned-store reply = %q", r)
	}
}

// TestCmdGoal_RespectsGoalsEnabledGate guards against IM bypassing the
// server's goal.enabled kill switch: unlike this, the REST and TUI surfaces
// both refuse to mutate goal state when it's off. Before SetGoalsEnabled
// existed, a goal set here would persist straight to the session's backing
// store and silently reactivate the moment a REST/TUI/Web surface later
// touched the same session.
func TestCmdGoal_RespectsGoalsEnabledGate(t *testing.T) {
	tempHome(t)
	ev := InboundEvent{Platform: "feishu", ChatID: "c2", UserID: "u2"}

	mgr := NewManager(&Config{}, fakeAgentFactory, BindByChatUser)
	mgr.SetGoalsEnabled(false)
	sess := mgr.GetOrCreateSession(ev)

	if r := mgr.cmdGoal(ev, "ship the release"); !strings.Contains(r, "disabled") {
		t.Errorf("goals-disabled reply = %q, want a disabled notice", r)
	}
	if _, ok := sess.GoalStore().GoalSnapshot(); ok {
		t.Error("goal must not be created on the backing store while goals are disabled")
	}

	// Re-enabling restores the normal behavior.
	mgr.SetGoalsEnabled(true)
	if r := mgr.cmdGoal(ev, "ship the release"); !strings.Contains(r, "Goal set") {
		t.Errorf("re-enabled create reply = %q", r)
	}
}
