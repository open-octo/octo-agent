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
