package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agentprofile"
)

// Minimal resultSpawner that returns a fixed reply.
type resultSpawnerSync struct {
	reply string
}

func (s *resultSpawnerSync) Spawn(ctx context.Context, req SpawnRequest) (SpawnResult, error) {
	return SpawnResult{Reply: s.reply}, nil
}

func (s *resultSpawnerSync) Continue(ctx context.Context, agentID, message string) (SpawnResult, error) {
	return SpawnResult{}, fmt.Errorf("continue not supported")
}

// TestAgentTool_SubagentTypeResolvesViaStore verifies that the sub_agent tool
// resolves subagent_type names against the agentprofile.Store (built-in +
// user-defined profiles), retiring the old agent_presets.go lookup.
func TestAgentTool_SubagentTypeResolvesViaStore(t *testing.T) {
	dir := t.TempDir()
	store := agentprofile.New(dir)

	ctx := WithProfileStore(context.Background(), store)
	mgr := NewSubAgentManager(&resultSpawnerSync{reply: "ok"})
	ctx = WithSubAgentManager(ctx, mgr)

	// Built-in profile resolves without any file.
	res, err := (AgentTool{}).Execute(ctx, "sub_agent", map[string]any{
		"description":   "d",
		"prompt":        "p",
		"subagent_type": "explore",
	})
	if err != nil {
		t.Fatalf("built-in 'explore' should resolve: %v", err)
	}
	if res.Text == "" {
		t.Fatal("expected non-empty result")
	}

	// User-defined profile (on disk) resolves too.
	if err := store.Create(&agentprofile.Profile{
		ID:          "my-reviewer",
		Description: "custom",
		CapabilitySpec: agentprofile.CapabilitySpec{
			SystemPrompt: "You review things.",
			ReadOnly:     true,
		},
	}); err != nil {
		t.Fatal(err)
	}
	res, err = (AgentTool{}).Execute(ctx, "sub_agent", map[string]any{
		"description":   "d",
		"prompt":        "p",
		"subagent_type": "my-reviewer",
	})
	if err != nil {
		t.Fatalf("user-defined 'my-reviewer' should resolve: %v", err)
	}
	if res.Text == "" {
		t.Fatal("expected non-empty result")
	}

	// Unknown type errors with available names.
	_, err = (AgentTool{}).Execute(ctx, "sub_agent", map[string]any{
		"description":   "d",
		"prompt":        "p",
		"subagent_type": "nonexistent",
	})
	if err == nil {
		t.Fatal("unknown subagent_type should error")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Fatalf("error should mention the unknown type, got: %v", err)
	}
	if !strings.Contains(err.Error(), "explore") {
		t.Errorf("error should list built-in profile 'explore' even when Store is wired, got: %v", err)
	}
}
