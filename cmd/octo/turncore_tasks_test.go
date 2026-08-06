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
func TestRunTurn_KeepsIncompletePlan(t *testing.T) {
	store := tasks.New()
	store.Create("pending task", "", "")
	tools.SetTaskStore(store)
	t.Cleanup(func() { tools.SetTaskStore(nil) })

	cs := &capturingSender{stubSender: stubSender{reply: "ok"}}
	a := agent.New(cs, "m")
	if _, err := runTurn(context.Background(), a, replConfig{a: a}, noopSink{}, "continue"); err != nil {
		t.Fatalf("runTurn: %v", err)
	}
	items := tools.ActiveTaskStore().List()
	if len(items) != 1 || items[0].Subject != "pending task" {
		t.Errorf("incomplete plan should carry over, got %+v", items)
	}
}
