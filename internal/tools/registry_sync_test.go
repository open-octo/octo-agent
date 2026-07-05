package tools

import (
	"context"
	"testing"
)

func advertisedNames() map[string]bool {
	m := map[string]bool{}
	for _, d := range DefaultToolsFor("") {
		m[d.Name] = true
	}
	return m
}

// TestSyncModeAdvertisesSubAgent verifies a synchronous manager (server / IM)
// advertises the sub_agent tool.
func TestSyncModeAdvertisesSubAgent(t *testing.T) {
	mgr := NewSubAgentManager(&fakeSpawner{})
	mgr.SetSynchronous(true)
	SetDefaultSubAgentManager(mgr)
	t.Cleanup(func() { SetDefaultSubAgentManager(nil) })

	names := advertisedNames()
	if !names["sub_agent"] {
		t.Error("sub_agent should be advertised in sync mode")
	}
}

// TestAsyncModeAdvertisesSubAgent verifies the interactive (async) default
// also advertises the sub_agent tool.
func TestAsyncModeAdvertisesSubAgent(t *testing.T) {
	mgr := NewSubAgentManager(&fakeSpawner{}) // async
	SetDefaultSubAgentManager(mgr)
	t.Cleanup(func() { SetDefaultSubAgentManager(nil) })

	names := advertisedNames()
	if !names["sub_agent"] {
		t.Error("sub_agent should be advertised in async mode")
	}
}

// fakeSpawner is a minimal Spawner implementation for tests.
type fakeSpawner struct{}

func (f *fakeSpawner) Spawn(_ context.Context, _ SpawnRequest) (SpawnResult, error) {
	return SpawnResult{}, nil
}

func (f *fakeSpawner) Continue(_ context.Context, _, _ string) (SpawnResult, error) {
	return SpawnResult{}, nil
}

// #1133: DefaultToolsForCtx must advertise sub_agent/workflow off a ctx-scoped
// SubAgentManager alone — no process-global spawner/manager involved — so a
// multi-session server can correctly gate advertisement per turn.
func TestDefaultToolsForCtx_AdvertisesFromCtxScopedManager(t *testing.T) {
	// Baseline: confirm the process-global slots are genuinely empty, so a
	// pass here can only be explained by the ctx-scoped manager.
	if spawnerEnabled() || subAgentManagerEnabled() {
		t.Fatal("test setup: process-global spawner/manager must be unset")
	}

	mgr := NewSubAgentManager(&fakeSpawner{})
	ctx := WithSubAgentManager(context.Background(), mgr)

	names := map[string]bool{}
	for _, d := range DefaultToolsForCtx(ctx, "") {
		names[d.Name] = true
	}
	if !names["sub_agent"] {
		t.Error("sub_agent should be advertised from a ctx-scoped manager alone")
	}
	if !names["workflow"] {
		t.Error("workflow should be advertised from a ctx-scoped manager's spawner alone")
	}

	// A ctx with no manager at all must fall back to the (empty) global gate,
	// exactly like DefaultToolsFor — ctx-awareness must not leak into an
	// unrelated ctx.
	plainNames := map[string]bool{}
	for _, d := range DefaultToolsForCtx(context.Background(), "") {
		plainNames[d.Name] = true
	}
	if plainNames["sub_agent"] || plainNames["workflow"] {
		t.Error("a ctx with no scoped manager must not advertise sub_agent/workflow")
	}
}

// A ctx-scoped manager with no spawner (nil) advertises sub_agent (it can
// still track/report sub-agents) but not workflow, which specifically needs a
// spawner to dispatch through.
func TestDefaultToolsForCtx_NilSpawnerWithholdsWorkflow(t *testing.T) {
	mgr := NewSubAgentManager(nil)
	ctx := WithSubAgentManager(context.Background(), mgr)

	names := map[string]bool{}
	for _, d := range DefaultToolsForCtx(ctx, "") {
		names[d.Name] = true
	}
	if !names["sub_agent"] {
		t.Error("sub_agent should still be advertised without a spawner")
	}
	if names["workflow"] {
		t.Error("workflow should be withheld when the ctx-scoped manager has no spawner")
	}
}

// DefaultToolsFor(model) must remain exactly DefaultToolsForCtx(background, model).
func TestDefaultToolsFor_MatchesCtxWithBackground(t *testing.T) {
	got := DefaultToolsFor("some-model")
	want := DefaultToolsForCtx(context.Background(), "some-model")
	if len(got) != len(want) {
		t.Fatalf("DefaultToolsFor returned %d tools, DefaultToolsForCtx(background) returned %d", len(got), len(want))
	}
	for i := range got {
		if got[i].Name != want[i].Name {
			t.Errorf("tool[%d] = %q, want %q", i, got[i].Name, want[i].Name)
		}
	}
}
