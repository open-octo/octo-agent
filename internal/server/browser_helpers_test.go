package server

import (
	"context"
	"testing"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/tools"
)

// TestPrepareToolTurn_WiresBrowserLLMHelpers guards the serve path: record_stop
// distillation and run_skill self-heal need a model, and serve wires tools in
// prepareToolTurn (not app.WireTools), so it must install the healer + skill
// generator — otherwise the web UI silently has no self-heal / LLM distill.
func TestPrepareToolTurn_WiresBrowserLLMHelpers(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	// Clear them, then confirm a tool turn installs them from the agent's sender.
	tools.SetBrowserHealer(nil)
	tools.SetBrowserSkillGenerator(nil)

	a := agent.New(&stubSender{}, "stub-model")
	if _, _, _, err := srv.prepareToolTurn(context.Background(), a); err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	if !tools.BrowserHealerSet() {
		t.Error("run_skill self-heal helper not wired in serve")
	}
	if !tools.BrowserSkillGeneratorSet() {
		t.Error("record_stop skill generator not wired in serve")
	}
}
