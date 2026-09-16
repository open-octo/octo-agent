package server

import (
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
)

// The CLI honors compact_auto_pct from config.yml (cmd/octo/chat.go); the
// server's buildAgent must too, or web/desktop/IM turns silently ignore the
// configured threshold and always run the built-in 75% default.
func TestBuildAgent_HonorsCompactConfig(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints:      []config.Endpoint{{ID: "ep-a", Provider: "openai", Models: []config.EndpointModel{{Model: "gpt-4o"}}}},
		Default:        "ep-a::gpt-4o",
		CompactAutoPct: 70,
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	a := srv.buildAgent(agent.NewSession("gpt-4o", ""))

	if want := float64(70) / 100.0; a.CompactAutoFraction != want {
		t.Errorf("CompactAutoFraction = %v, want %v (from compact_auto_pct: 70)", a.CompactAutoFraction, want)
	}
}

// With no compaction settings in config, buildAgent leaves the auto-fraction at
// zero so the agent falls back to its built-in default (75%) — not disabled.
func TestBuildAgent_CompactDefaultsWhenUnset(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{{ID: "ep-a", Provider: "openai", Models: []config.EndpointModel{{Model: "gpt-4o"}}}},
		Default:   "ep-a::gpt-4o",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	a := srv.buildAgent(agent.NewSession("gpt-4o", ""))

	if a.CompactAutoFraction != 0 {
		t.Errorf("CompactAutoFraction = %v, want 0 (unset → agent's built-in default)", a.CompactAutoFraction)
	}
}

// The server has its own install points for the fallback window; a config-only
// implementation in cmd/octo would leave every web/desktop/IM turn on the
// built-in default. Same class of miss the compact_auto_pct tests above guard.
//
// The window is process-global, so each case restores it.
func TestBuildAgent_HonorsFallbackContextWindow(t *testing.T) {
	t.Cleanup(func() { agent.SetFallbackContextWindow(0) })
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints:             []config.Endpoint{{ID: "ep-a", Provider: "openai", Models: []config.EndpointModel{{Model: "gpt-4o"}}}},
		Default:               "ep-a::gpt-4o",
		FallbackContextWindow: 24_000,
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	srv.buildAgent(agent.NewSession("gpt-4o", ""))

	if got := agent.FallbackContextWindow(); got != 24_000 {
		t.Errorf("FallbackContextWindow() = %d, want 24000 (from fallback_context_window)", got)
	}
	// It reaches what it exists for: an unknown model's window.
	if got := agent.ContextWindow("some-internal-llm"); got != 24_000 {
		t.Errorf("unknown model window = %d, want 24000", got)
	}
	// And still leaves a known model alone.
	if got := agent.ContextWindow("claude-sonnet-5"); got != 1_000_000 {
		t.Errorf("known model window = %d, want 1000000", got)
	}
}

// The floor has to hold on the server path too. config.Load never calls
// Validate, so without the shared resolver a "32" in config.yml would install
// literally 32 tokens here while the CLI rejected it — the same file behaving
// differently depending on which entry point read it.
func TestBuildAgent_FallbackContextWindowFloor(t *testing.T) {
	t.Cleanup(func() { agent.SetFallbackContextWindow(0) })
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints:             []config.Endpoint{{ID: "ep-a", Provider: "openai", Models: []config.EndpointModel{{Model: "gpt-4o"}}}},
		Default:               "ep-a::gpt-4o",
		FallbackContextWindow: 32, // 32k written as 32
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	srv.buildAgent(agent.NewSession("gpt-4o", ""))

	if got := agent.FallbackContextWindow(); got != 128_000 {
		t.Errorf("FallbackContextWindow() = %d, want the built-in 128000 (32 is the unit mistake)", got)
	}
}

// OCTO_FALLBACK_CONTEXT_WINDOW has to work under `octo serve`, which never
// reaches the CLI's flag resolver. ~/.octo/serve.env is the documented way to
// give a GUI- or init-launched server its environment, so this is the path a
// self-hosted deployment actually uses.
func TestBuildAgent_FallbackContextWindowFromEnv(t *testing.T) {
	t.Cleanup(func() { agent.SetFallbackContextWindow(0) })
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{{ID: "ep-a", Provider: "openai", Models: []config.EndpointModel{{Model: "gpt-4o"}}}},
		Default:   "ep-a::gpt-4o",
	})
	t.Setenv("OCTO_FALLBACK_CONTEXT_WINDOW", "24000")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	srv.buildAgent(agent.NewSession("gpt-4o", ""))

	if got := agent.FallbackContextWindow(); got != 24_000 {
		t.Errorf("FallbackContextWindow() = %d, want 24000 from the environment", got)
	}
}
