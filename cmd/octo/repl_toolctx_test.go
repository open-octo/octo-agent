package main

import (
	"context"
	"testing"

	"github.com/open-octo/octo-agent/internal/agentprofile"
	"github.com/open-octo/octo-agent/internal/tools"
)

// Every recompute of cfg.tools reads toolContext(); a nil ctx there would
// panic on the first Value lookup, and the zero replConfig is what tests and
// hand-built configs use.
func TestReplConfig_ToolContext_FallsBackWhenUnset(t *testing.T) {
	var cfg replConfig
	if got := cfg.toolContext(); got == nil {
		t.Fatal("toolContext returned nil for a zero config")
	}
	if got := (*replConfig)(nil).toolContext(); got == nil {
		t.Fatal("toolContext returned nil for a nil receiver")
	}
}

// The wired context must reach the builders unchanged — substituting a fresh
// one would silently downgrade sub_agent's schema to the store-less form.
func TestReplConfig_ToolContext_ReturnsTheWiredContext(t *testing.T) {
	store := agentprofile.New(t.TempDir())
	wired := tools.WithProfileStore(context.Background(), store)
	cfg := replConfig{toolCtx: wired}

	if got := cfg.toolContext(); got != wired {
		t.Error("toolContext did not return the wired context")
	}
}
