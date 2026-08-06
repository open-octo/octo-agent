package main

import (
	"context"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tasks"
	"github.com/open-octo/octo-agent/internal/tools"
)

// TestRunTurn_ClearsCompletedPlanAtStart verifies the TUI-side clear-and-rebuild
// (mirroring the server's prepareToolTurn): a fully-completed plan is dropped
// when the next turn starts, so the new turn's tasks build a fresh list instead
// of piling onto old, done ones. Without this, the accumulated task list grows
// unboundedly across turns.
func TestRunTurn_ClearsCompletedPlanAtStart(t *testing.T) {
	store := tasks.New()
	id, _ := store.Create("a", "", "")
	done := tasks.Completed
	if _, err := store.Update(id, tasks.UpdateField{Status: &done}); err != nil {
		t.Fatalf("update: %v", err)
	}
	tools.SetTaskStore(store)
	t.Cleanup(func() { tools.SetTaskStore(nil) })

	cs := &capturingSender{stubSender: stubSender{reply: "ok"}}
	a := agent.New(cs, "m")
	if _, err := runTurn(context.Background(), a, replConfig{a: a}, noopSink{}, "next"); err != nil {
		t.Fatalf("runTurn: %v", err)
	}
	if got := tools.ActiveTaskStore(); got == nil {
		t.Fatal("task store should remain active after the clear")
	} else if items := got.List(); len(items) != 0 {
		t.Errorf("completed plan should be cleared at turn start, got %d tasks: %+v", len(items), items)
	}
}

// TestRunTurn_KeepsIncompletePlan verifies an unfinished plan carries over to
// the next turn — the agent keeps working on it instead of losing progress.
// The fixture is a partially-done plan (one completed + one pending), the
// trickiest case: it must NOT be cleared.
func TestRunTurn_KeepsIncompletePlan(t *testing.T) {
	store := tasks.New()
	doneID, _ := store.Create("done task", "", "")
	done := tasks.Completed
	if _, err := store.Update(doneID, tasks.UpdateField{Status: &done}); err != nil {
		t.Fatalf("update: %v", err)
	}
	store.Create("pending task", "", "")
	tools.SetTaskStore(store)
	t.Cleanup(func() { tools.SetTaskStore(nil) })

	cs := &capturingSender{stubSender: stubSender{reply: "ok"}}
	a := agent.New(cs, "m")
	if _, err := runTurn(context.Background(), a, replConfig{a: a}, noopSink{}, "continue"); err != nil {
		t.Fatalf("runTurn: %v", err)
	}
	items := tools.ActiveTaskStore().List()
	if len(items) != 2 {
		t.Fatalf("partially-done plan should carry over untouched, got %d tasks: %+v", len(items), items)
	}
	for _, want := range []string{"done task", "pending task"} {
		found := false
		for _, it := range items {
			if it.Subject == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("plan lost task %q; got %+v", want, items)
		}
	}
}

// TestRunTurn_NoopWithoutTaskStore guards the disabled-session invariant
// end-to-end: when no store is registered (nil), a turn must leave it nil —
// the clear-and-rebuild must never enable task tracking.
func TestRunTurn_NoopWithoutTaskStore(t *testing.T) {
	tools.SetTaskStore(nil)
	t.Cleanup(func() { tools.SetTaskStore(nil) })

	cs := &capturingSender{stubSender: stubSender{reply: "ok"}}
	a := agent.New(cs, "m")
	if _, err := runTurn(context.Background(), a, replConfig{a: a}, noopSink{}, "next"); err != nil {
		t.Fatalf("runTurn: %v", err)
	}
	if tools.ActiveTaskStore() != nil {
		t.Error("disabled session (nil store) must stay nil — the clear must not enable task tracking")
	}
}
