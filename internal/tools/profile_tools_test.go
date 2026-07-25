package tools

import (
	"context"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/agentprofile"
)

// ctxKeyProfileStore and ctxKeySessionAgentID are defined in registry.go.

func TestDefaultToolsForProfile_NoProfileReturnsAll(t *testing.T) {
	ctx := context.Background()
	all := DefaultToolsForProfile(ctx, "test-model")
	if len(all) == 0 {
		t.Fatal("expected non-empty tool list")
	}
	// No store/agentID in context → all tools returned.
	before := len(all)
	if before < 5 {
		t.Fatalf("expected several tools, got %d", before)
	}
}

func TestDefaultToolsForProfile_FiltersByAllowlist(t *testing.T) {
	dir := t.TempDir()
	store := agentprofile.New(dir, nil)
	// Create a profile with a restricted tool allowlist.
	// (Writing the file and relying on read-through scan.)
	if err := store.Create(&agentprofile.Profile{
		ID:          "restricted",
		Description: "only read_file and grep",
		CapabilitySpec: agentprofile.CapabilitySpec{
			Tools: []string{"read_file", "grep"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	ctx := WithSessionAgentID(WithProfileStore(context.Background(), store), "restricted")
	filtered := DefaultToolsForProfile(ctx, "test-model")
	// Don't assert exact count — defaultToolsFor gates tools on process-global
	// flags, which test order can affect. Verify presence instead.
	haveReadFile, haveGrep := false, false
	for _, d := range filtered {
		if d.Name == "read_file" {
			haveReadFile = true
		}
		if d.Name == "grep" {
			haveGrep = true
		}
		if d.Name != "read_file" && d.Name != "grep" {
			t.Errorf("unexpected tool %q in filtered list", d.Name)
		}
	}
	if !haveReadFile || !haveGrep {
		t.Fatalf("filtered tools missing expected tools (have read_file=%v grep=%v): %+v", haveReadFile, haveGrep, toolNames(filtered))
	}
}

func TestDefaultToolsForProfile_EmptyToolsReturnsAll(t *testing.T) {
	dir := t.TempDir()
	store := agentprofile.New(dir, nil)
	if err := store.Create(&agentprofile.Profile{
		ID:          "full",
		Description: "all tools",
		CapabilitySpec: agentprofile.CapabilitySpec{
			Tools: []string{}, // empty = all
		},
	}); err != nil {
		t.Fatal(err)
	}
	ctx := WithSessionAgentID(WithProfileStore(context.Background(), store), "full")
	all := DefaultToolsForProfile(ctx, "test-model")
	if len(all) < 5 {
		t.Fatalf("expected several tools with empty allowlist, got %d", len(all))
	}
}

func TestDefaultToolsForProfile_UnknownAgentFallsBack(t *testing.T) {
	dir := t.TempDir()
	store := agentprofile.New(dir, nil)
	// No profile for "ghost" → store.Get misses → all tools.
	ctx := WithSessionAgentID(WithProfileStore(context.Background(), store), "ghost")
	all := DefaultToolsForProfile(ctx, "test-model")
	if len(all) < 5 {
		t.Fatalf("expected all tools for unknown agent, got %d", len(all))
	}
}

func TestDefaultToolsForProfile_SubToolsFiltered(t *testing.T) {
	// Verify that sub_agent tools (which require a SubAgentManager in ctx) are
	// filtered out when the profile doesn't include them.
	dir := t.TempDir()
	store := agentprofile.New(dir, nil)
	if err := store.Create(&agentprofile.Profile{
		ID:          "no-sub",
		Description: "no sub-agent",
		CapabilitySpec: agentprofile.CapabilitySpec{
			Tools: []string{"read_file", "grep", "terminal"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	ctx := WithSessionAgentID(WithProfileStore(context.Background(), store), "no-sub")
	filtered := DefaultToolsForProfile(ctx, "test-model")
	for _, d := range filtered {
		if d.Name == "sub_agent" || d.Name == "sub_agent_status" {
			t.Errorf("sub-agent tool %q should be filtered out", d.Name)
		}
	}
}

func toolNames(defs []agent.ToolDefinition) []string {
	var out []string
	for _, d := range defs {
		out = append(out, d.Name)
	}
	return out
}
