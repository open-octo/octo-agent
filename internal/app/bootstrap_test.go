package app

import (
	"context"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/tools"
)

func TestWireTools_SetsUpEnvAndCleansUp(t *testing.T) {
	// Reset the process globals afterwards regardless of what the test asserts.
	t.Cleanup(func() { tools.SetSpawner(nil); tools.SetTaskStore(nil) })

	a := agent.New(sender{p: &mockProvider{}}, "claude-haiku-4-5")
	env, cleanup := WireTools(a, true)

	if tools.ActiveSpawner() == nil {
		t.Error("WireTools should register a spawner so sub_agent appears in the catalog")
	}
	if tools.ActiveTaskStore() == nil {
		t.Error("WireTools(enableTasks=true) should install a task store")
	}
	if env.SubAgentMgr == nil {
		t.Error("WireTools should return a sub-agent manager")
	}
	// The sub-agent manager must be registered globally so that DefaultToolsFor
	// advertises the sub_agent tool to the LLM.
	hasSubAgent := false
	for _, d := range env.ToolsFor(context.Background()) {
		if d.Name == "sub_agent" {
			hasSubAgent = true
			break
		}
	}
	if !hasSubAgent {
		t.Error("WireTools should advertise the sub_agent tool in the catalog")
	}
	if env.ToolsFor == nil {
		t.Fatal("WireTools should return a tool-list function")
	}
	// ToolsFor is model-aware and must at least return the core built-ins.
	if len(env.ToolsFor(context.Background())) == 0 {
		t.Error("ToolsFor returned an empty tool list")
	}

	cleanup()
	if tools.ActiveSpawner() != nil {
		t.Error("cleanup should reset the spawner")
	}
	if tools.ActiveTaskStore() != nil {
		t.Error("cleanup should reset the task store")
	}
	// After cleanup, the sub_agent tool should no longer be advertised.
	for _, d := range env.ToolsFor(context.Background()) {
		if d.Name == "sub_agent" {
			t.Error("cleanup should reset the sub-agent manager so sub_agent is no longer advertised")
			break
		}
	}
}

func TestWireTools_TasksDisabled(t *testing.T) {
	t.Cleanup(func() { tools.SetSpawner(nil); tools.SetTaskStore(nil) })

	a := agent.New(sender{p: &mockProvider{}}, "claude-haiku-4-5")
	_, cleanup := WireTools(a, false)
	defer cleanup()

	if tools.ActiveTaskStore() != nil {
		t.Error("WireTools(enableTasks=false) must not install a task store")
	}
	if tools.ActiveSpawner() == nil {
		t.Error("spawner should still be registered when tasks are disabled")
	}
}

func TestWireTools_WiresMemoryBackendFromConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Cleanup(func() { tools.SetSpawner(nil); tools.SetTaskStore(nil); tools.SetMemoryBackend(nil) })

	cfg := config.Config{
		MemoryBackend: config.MemoryBackendConfig{Type: "hindsight", BaseURL: "http://localhost:8888"},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	a := agent.New(sender{p: &mockProvider{}}, "claude-haiku-4-5")
	_, cleanup := WireTools(a, false)

	if g := tools.MemoryBackendGuidance(); g == "" {
		t.Error("WireTools should register the configured memory backend")
	}

	cleanup()
	if g := tools.MemoryBackendGuidance(); g != "" {
		t.Errorf("cleanup should reset the memory backend, guidance = %q", g)
	}
}

func TestWireTools_NoMemoryBackendConfigured(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Cleanup(func() { tools.SetSpawner(nil); tools.SetTaskStore(nil); tools.SetMemoryBackend(nil) })

	a := agent.New(sender{p: &mockProvider{}}, "claude-haiku-4-5")
	_, cleanup := WireTools(a, false)
	defer cleanup()

	if g := tools.MemoryBackendGuidance(); g != "" {
		t.Errorf("guidance = %q, want empty with no memory_backend configured", g)
	}
}
