package app

import (
	"context"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tasks"
	"github.com/open-octo/octo-agent/internal/tools"
)

// The session tool env owns the per-session task store, so it also owns the
// guard that closes the checklist out at turn end — otherwise a web/SDK turn
// would leave a finished plan showing an in_progress step.
func TestNewSessionToolEnv_WiresTurnEndTaskGuard(t *testing.T) {
	const sid = "sess-turn-end-guard"
	t.Cleanup(func() {
		tools.CloseSessionTaskStore(sid)
		tools.CloseSessionSubAgentManager(sid)
		tools.CloseSessionBackgroundManager(sid)
		tools.CloseSessionWorkflowManager(sid)
		tools.SetTaskStore(nil)
	})

	a := agent.New(sender{p: &mockProvider{}}, "claude-haiku-4-5")
	executor := tools.NewDefaultRegistry()
	ctx, _, _, cleanup := NewSessionToolEnv(context.Background(), a, sid, executor, ToolEnvCallbacks{})

	if a.TurnEndReminder == nil {
		t.Fatal("NewSessionToolEnv should wire the turn-end task guard")
	}
	planTools := []string{"task_update"}
	// Quiet while the plan is empty…
	if got := a.TurnEndReminder(ctx, planTools); got != "" {
		t.Errorf("empty plan should not fire the guard, got %q", got)
	}
	// …and it reads the same session store the task_* tools write to.
	store := tools.SessionTaskStore(sid)
	id, err := store.Create("ship it", "", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	st := tasks.InProgress
	if _, err := store.Update(id, tasks.UpdateField{Status: &st}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := a.TurnEndReminder(ctx, planTools); got == "" {
		t.Error("a task left in_progress should fire the guard")
	}
	// …and cleanup takes it back off, so an SDK caller reusing one agent across
	// sessions doesn't inherit a stale guard.
	cleanup()
	if a.TurnEndReminder != nil {
		t.Error("cleanup should clear the turn-end task guard")
	}
}
