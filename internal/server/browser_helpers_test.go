package server

import (
	"context"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// TestPrepareToolTurn_WiresBrowserLLMHelpers guards the serve path: record_stop
// distillation and replay self-heal need a model, and serve wires tools in
// prepareToolTurn (not app.WireTools), so it must install the healer + recording
// generator — otherwise the web UI silently has no self-heal / LLM distill.
func TestPrepareToolTurn_WiresBrowserLLMHelpers(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	// Clear them, then confirm a tool turn installs them from the agent's sender.
	tools.SetBrowserHealer(nil)
	tools.SetBrowserRecordingGenerator(nil)

	a := agent.New(&stubSender{}, "stub-model")
	if _, _, _, _, err := srv.prepareToolTurn(context.Background(), a, nil); err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	if !tools.BrowserHealerSet() {
		t.Error("replay self-heal helper not wired in serve")
	}
	if !tools.BrowserRecordingGeneratorSet() {
		t.Error("record_stop recording generator not wired in serve")
	}
}
