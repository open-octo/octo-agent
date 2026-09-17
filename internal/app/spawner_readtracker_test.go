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
