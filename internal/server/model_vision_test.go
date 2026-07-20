package server

import (
	"context"
	"sync"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/tools"
)

// TestPrepareToolTurn_WiresModelVision guards the serve path: unlike the CLI
// (app.WireTools), the server wires tools in prepareToolTurn, so it must stamp
// the vision setting for the active model into the turn's ctx — otherwise a
// text-only model gets handed a screenshot or read_file image block it
// rejects (HTTP 400).
func TestPrepareToolTurn_WiresModelVision(t *testing.T) {
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

	ctx, _, _, _, err := srv.prepareToolTurn(context.Background(), agent.New(&stubSender{}, "qwen3.7-max"), nil)
	if err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	if tools.ModelVisionEnabled(ctx) {
		t.Error("vision should be OFF for a text-only model (qwen3.7-max)")
	}

	ctx, _, _, _, err = srv.prepareToolTurn(context.Background(), agent.New(&stubSender{}, "gpt-4o"), nil)
	if err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	if !tools.ModelVisionEnabled(ctx) {
		t.Error("vision should be ON for a vision model (gpt-4o)")
	}
}

// TestPrepareToolTurn_ModelVisionConcurrentSessions proves the fix for the
// race the old tools.SetBrowserVision process-global had: two sessions on
// different models, wired concurrently, must each see their own model's
// vision setting rather than whichever call last won the global write.
func TestPrepareToolTurn_ModelVisionConcurrentSessions(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-a", Provider: "openai", Models: []config.EndpointModel{
				{Model: "qwen3.7-max", Vision: false},
				{Model: "gpt-4o", Vision: true},
			}},
		},
		Default: "ep-a::qwen3.7-max",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	const rounds = 50
	var wg sync.WaitGroup
	errs := make(chan string, rounds*2)
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			ctx, _, _, _, err := srv.prepareToolTurn(context.Background(), agent.New(&stubSender{}, "qwen3.7-max"), nil)
			if err != nil {
				errs <- err.Error()
				continue
			}
			if tools.ModelVisionEnabled(ctx) {
				errs <- "qwen3.7-max turn saw vision ON"
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			ctx, _, _, _, err := srv.prepareToolTurn(context.Background(), agent.New(&stubSender{}, "gpt-4o"), nil)
			if err != nil {
				errs <- err.Error()
				continue
			}
			if !tools.ModelVisionEnabled(ctx) {
				errs <- "gpt-4o turn saw vision OFF"
			}
		}
	}()
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
