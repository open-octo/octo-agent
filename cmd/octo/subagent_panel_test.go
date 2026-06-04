package main

import (
	"testing"

	"github.com/Leihb/octo-agent/internal/tools"
)

// newPanelModel builds a tuiModel with just the sub-agent panel state, enough to
// exercise the fold/remove logic without a bubbletea program.
func newPanelModel() *tuiModel {
	return &tuiModel{subAgents: map[string]*subAgentUI{}}
}

func TestSubAgentPanel_StartedThenTools(t *testing.T) {
	m := newPanelModel()
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Description: "Find code", Kind: "started"})
	if len(m.subAgentOrder) != 1 || m.subAgentOrder[0] != "agent_1" {
		t.Fatalf("order = %v, want [agent_1]", m.subAgentOrder)
	}
	for _, name := range []string{"read_file", "grep", "read_file"} {
		m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Kind: "tool", ToolName: name})
	}
	sa := m.subAgents["agent_1"]
	if sa.toolCount != 3 {
		t.Errorf("toolCount = %d, want 3", sa.toolCount)
	}
	if sa.description != "Find code" {
		t.Errorf("description = %q", sa.description)
	}
	if got := sa.recent[len(sa.recent)-1]; got != "read_file" {
		t.Errorf("last recent = %q, want read_file", got)
	}
}

func TestSubAgentPanel_RecentCapped(t *testing.T) {
	m := newPanelModel()
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Kind: "started"})
	for i := 0; i < maxSubAgentRecentTools+3; i++ {
		m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Kind: "tool", ToolName: "t"})
	}
	if got := len(m.subAgents["agent_1"].recent); got != maxSubAgentRecentTools {
		t.Errorf("recent len = %d, want %d (capped)", got, maxSubAgentRecentTools)
	}
}

func TestSubAgentPanel_ToolError(t *testing.T) {
	m := newPanelModel()
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Kind: "started"})
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Kind: "tool_error", ToolName: "terminal"})
	sa := m.subAgents["agent_1"]
	if !sa.errored {
		t.Error("errored flag not set")
	}
	if got := sa.recent[len(sa.recent)-1]; got != "terminal ✗" {
		t.Errorf("recent = %q, want 'terminal ✗'", got)
	}
}

func TestSubAgentPanel_RemoveOnComplete(t *testing.T) {
	m := newPanelModel()
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Kind: "started"})
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_2", Kind: "started"})
	m.removeSubAgent("agent_1")
	if _, ok := m.subAgents["agent_1"]; ok {
		t.Error("agent_1 still present after remove")
	}
	if len(m.subAgentOrder) != 1 || m.subAgentOrder[0] != "agent_2" {
		t.Errorf("order = %v, want [agent_2]", m.subAgentOrder)
	}
}

func TestSubAgentPanel_StartedResetsChain(t *testing.T) {
	m := newPanelModel()
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Kind: "started"})
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Kind: "tool", ToolName: "grep"})
	// A second round (Agent) re-starts: chain resets, slot stays.
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Kind: "started"})
	sa := m.subAgents["agent_1"]
	if sa.toolCount != 0 || len(sa.recent) != 0 {
		t.Errorf("chain not reset on re-start: count=%d recent=%v", sa.toolCount, sa.recent)
	}
	if len(m.subAgentOrder) != 1 {
		t.Errorf("order changed on re-start: %v", m.subAgentOrder)
	}
}
