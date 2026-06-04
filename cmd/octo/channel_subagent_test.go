package main

import (
	"testing"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/tools"
)

// TestRegisterChannelSubAgentToolsAdvertises verifies the IM bridge advertises
// the sub-agent and task tools once the gating sentinels are registered (they'd
// otherwise be withheld by DefaultToolsFor), and that the cleanup clears them.
func TestRegisterChannelSubAgentToolsAdvertises(t *testing.T) {
	template := agent.New(&stubSender{}, "test-model")

	cleanup := registerChannelSubAgentTools(template)
	defer cleanup()

	names := map[string]bool{}
	for _, d := range tools.DefaultToolsFor("") {
		names[d.Name] = true
	}
	for _, want := range []string{"Agent", "Agent", "task_create", "task_list"} {
		if !names[want] {
			t.Errorf("expected %q advertised after registerChannelSubAgentTools", want)
		}
	}

	cleanup()
	cleared := map[string]bool{}
	for _, d := range tools.DefaultToolsFor("") {
		cleared[d.Name] = true
	}
	if cleared["Agent"] {
		t.Error("expected Agent withdrawn after cleanup")
	}
}
