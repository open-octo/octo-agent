package server

import (
	"context"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/tools"
)

// TestPrepareToolTurn_WiresBrowserVision guards the serve path: unlike the CLI
// (app.WireTools), the server wires tools in prepareToolTurn, so it must set the
// browser vision flag from the active model — otherwise a text-only model gets
// handed a screenshot it rejects (HTTP 400).
func TestPrepareToolTurn_WiresBrowserVision(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-a", Provider: "openai", Models: []config.EndpointModel{
				{Model: "qwen3.7-max", Vision: false}, // recorded text-only
				{Model: "gpt-4o", Vision: true},       // recorded vision
			}},
		},
		Default: "ep-a::qwen3.7-max",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	tools.SetBrowserVision(true) // start opposite of expected to prove it's set
	if _, _, _, _, err := srv.prepareToolTurn(context.Background(), agent.New(&stubSender{}, "qwen3.7-max"), nil); err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	if tools.BrowserVisionEnabled() {
		t.Error("vision should be OFF for a text-only model (qwen3.7-max)")
	}

	tools.SetBrowserVision(false)
	if _, _, _, _, err := srv.prepareToolTurn(context.Background(), agent.New(&stubSender{}, "gpt-4o"), nil); err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	if !tools.BrowserVisionEnabled() {
		t.Error("vision should be ON for a vision model (gpt-4o)")
	}
}
