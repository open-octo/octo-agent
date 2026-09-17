package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// A sub-agent reasons over its own history, so the read-before-write gate has
// to be per child: neither the parent's reads nor a sibling's may stand in for
// one the child never made.
func TestAgentSpawner_ChildGetsIndependentReadTracker(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	parentReg := tools.NewDefaultRegistry()
	if _, err := parentReg.Execute(ctx, "read_file", map[string]any{"path": p}); err != nil {
		t.Fatalf("parent read_file: %v", err)
	}

	send := &subAgentSender{reply: "ok"}
	parent := agent.New(send, "parent-model")
	sp := NewSpawner(parent, parentReg, func(context.Context) []agent.ToolDefinition { return nil })

	spawnChild := func() *liveChild {
		t.Helper()
		res, err := sp.Spawn(ctx, tools.SpawnRequest{Description: "d", Prompt: "p"})
		if err != nil {
			t.Fatal(err)
		}
		lc, ok := sp.reg.get(res.AgentID)
		if !ok {
			t.Fatalf("child %q not registered", res.AgentID)
		}
		return lc
	}

	editAs := func(lc *liveChild) error {
		_, err := lc.executor.Execute(ctx, "edit_file", map[string]any{
			"path": p, "old_string": "package x", "new_string": "package y",
		})
		return err
	}

	first := spawnChild()
	if err := editAs(first); err == nil || !strings.Contains(err.Error(), "not been read") {
		t.Errorf("child must not inherit the parent's read, got %v", err)
	}

	// Now the first child reads it for itself — that must not unlock the file
	// for the next child either.
	if _, err := first.executor.Execute(ctx, "read_file", map[string]any{"path": p}); err != nil {
		t.Fatalf("child read_file: %v", err)
	}
	if err := editAs(first); err != nil {
		t.Errorf("child's own read should let it edit: %v", err)
	}

	second := spawnChild()
	if err := editAs(second); err == nil || !strings.Contains(err.Error(), "not been read") {
		t.Errorf("a sibling's read must not carry over, got %v", err)
	}
}

// The fork above only happens when the executor implements
// tools.TrackerForking; anything wrapped around the registry that dropped the
// method would put every sub-agent back on the session's tracker without a
// word. Assert the executor the real CLI assembly hands the spawner.
func TestWireTools_ExecutorForksReadTracker(t *testing.T) {
	t.Cleanup(func() { tools.SetSpawner(nil); tools.SetTaskStore(nil) })

	a := agent.New(sender{p: &mockProvider{}}, "claude-haiku-4-5")
	env, cleanup := WireTools(a, false)
	defer cleanup()

	// Through the interface the spawner actually receives it as: ToolEnv.Executor
	// is concrete today, so a wrapper would have to change that type — and this
	// is what catches it when one does.
	var exec agent.ToolExecutor = env.Executor
	if _, ok := exec.(tools.TrackerForking); !ok {
		t.Fatalf("WireTools executor is %T, which does not implement tools.TrackerForking — sub-agents would inherit the session's reads", exec)
	}
}
