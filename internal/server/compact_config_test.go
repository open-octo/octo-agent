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
