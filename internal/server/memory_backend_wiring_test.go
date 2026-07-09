package server

import (
	"context"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/hooks"
	"github.com/open-octo/octo-agent/internal/tools"
)

// TestPrepareToolTurn_WiresMemoryBackend guards the serve path: unlike the CLI
// (app.WireTools), the server wires tools in prepareToolTurn, so it must build
// and register the configured memory backend itself — otherwise the feature
// silently doesn't exist under `octo serve` even when ~/.octo/config.yml
// configures one.
func TestPrepareToolTurn_WiresMemoryBackend(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Models:       []config.ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "gpt-4o",
		MemoryBackend: config.MemoryBackendConfig{
			Type:    "hindsight",
			BaseURL: "http://localhost:8888",
		},
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	tools.SetMemoryBackend(nil) // start disabled to prove prepareToolTurn is what enables it
	t.Cleanup(func() { tools.SetMemoryBackend(nil) })

	if _, _, _, _, err := srv.prepareToolTurn(context.Background(), agent.New(&stubSender{}, "gpt-4o"), nil); err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	if g := tools.MemoryBackendGuidance(); g == "" {
		t.Error("memory backend guidance is empty after prepareToolTurn; want it wired from config")
	}
}

func TestPrepareToolTurn_WiresAutoRecall(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Models:       []config.ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "gpt-4o",
		MemoryBackend: config.MemoryBackendConfig{
			Type:       "hindsight",
			BaseURL:    "http://localhost:8888",
			AutoRecall: true,
		},
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	tools.SetMemoryBackend(nil)
	tools.SetMemoryBackendAutoRecall(false)
	t.Cleanup(func() {
		tools.SetMemoryBackend(nil)
		tools.SetMemoryBackendAutoRecall(false)
	})

	if _, _, _, _, err := srv.prepareToolTurn(context.Background(), agent.New(&stubSender{}, "gpt-4o"), nil); err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}

	e := hooks.NewEngine(nil)
	tools.RegisterMemoryBackendHooks(e)
	if !e.Configured(hooks.EventUserPromptSubmit) {
		t.Error("prepareToolTurn should propagate auto_recall: true into an UserPromptSubmit hook")
	}
}

// TestBuildAgent_RefreshesAutoRecallBeforeRegisteringHooks guards against a
// real regression: buildAgent reads tools.MemoryBackendGuidance() and calls
// tools.RegisterMemoryBackendHooks BEFORE prepareToolTurn ever runs (in every
// caller — runTurn, the WS handler, the scheduled-task handler — buildAgent
// executes first in the same function). Relying on prepareToolTurn alone to
// keep the auto-recall flag fresh left a one-turn-stale window: a session
// hitting buildAgent right after auto_recall flips to true in config would
// still get built with the hook missing, because the global the previous
// turn's prepareToolTurn call last set was false. This deliberately leaves
// the global at false before calling buildAgent, with no prepareToolTurn call
// at all, to prove buildAgent's own refresh (not prepareToolTurn's) is what
// makes this work.
func TestBuildAgent_RefreshesAutoRecallBeforeRegisteringHooks(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Models:       []config.ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "gpt-4o",
		MemoryBackend: config.MemoryBackendConfig{
			Type:       "hindsight",
			BaseURL:    "http://localhost:8888",
			AutoRecall: true,
		},
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	tools.SetMemoryBackend(nil)
	tools.SetMemoryBackendAutoRecall(false) // simulate the previous turn's stale state
	t.Cleanup(func() {
		tools.SetMemoryBackend(nil)
		tools.SetMemoryBackendAutoRecall(false)
	})

	sess := agent.NewSession("gpt-4o", "")
	srv.buildAgent(sess)

	// Don't inspect a.Hooks directly — the MEMORY.md injector also registers
	// a hook on EventUserPromptSubmit, so Configured() there would pass even
	// if buildAgent never refreshed the auto-recall flag at all. Build a
	// fresh, unrelated engine instead: RegisterMemoryBackendHooks reads the
	// package-global auto-recall flag at registration time, so this proves
	// buildAgent left that global set to true — not just that some other
	// unrelated hook happens to share the event.
	e := hooks.NewEngine(nil)
	tools.RegisterMemoryBackendHooks(e)
	if !e.Configured(hooks.EventUserPromptSubmit) {
		t.Error("buildAgent should have refreshed the auto-recall global from this turn's config, not left the stale previous value")
	}
}

func TestPrepareToolTurn_NoMemoryBackendConfigured(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Models:       []config.ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "gpt-4o",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	tools.SetMemoryBackend(nil)
	t.Cleanup(func() { tools.SetMemoryBackend(nil) })

	if _, _, _, _, err := srv.prepareToolTurn(context.Background(), agent.New(&stubSender{}, "gpt-4o"), nil); err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	if g := tools.MemoryBackendGuidance(); g != "" {
		t.Errorf("memory backend guidance = %q, want empty with no memory_backend configured", g)
	}
}
