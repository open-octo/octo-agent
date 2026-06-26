package main

import (
	"testing"

	"github.com/Leihb/octo-agent/internal/tools"
	tea "github.com/charmbracelet/bubbletea"
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

func TestSubAgentPanel_CapturesAgentType(t *testing.T) {
	m := newPanelModel()
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Description: "Find code", AgentType: "explore", Kind: "started"})
	sa := m.subAgents["agent_1"]
	if sa.agentType != "explore" {
		t.Errorf("agentType = %q, want %q", sa.agentType, "explore")
	}
	// A later round with the type omitted keeps the captured type.
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Kind: "started"})
	if sa.agentType != "explore" {
		t.Errorf("agentType reset on re-start = %q, want %q", sa.agentType, "explore")
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

func TestSubAgentPanel_HistoryAccumulates(t *testing.T) {
	m := newPanelModel()
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Kind: "started"})
	for _, name := range []string{"read_file", "grep", "read_file", "edit_file", "terminal"} {
		m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Kind: "tool", ToolName: name})
	}
	sa := m.subAgents["agent_1"]
	if len(sa.history) != 5 {
		t.Errorf("history len = %d, want 5", len(sa.history))
	}
	// recent is still capped
	if len(sa.recent) != maxSubAgentRecentTools {
		t.Errorf("recent len = %d, want %d", len(sa.recent), maxSubAgentRecentTools)
	}
}

func TestSubAgentPanel_ExpandToggle(t *testing.T) {
	m := newPanelModel()
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Kind: "started"})
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Kind: "tool", ToolName: "grep"})
	m.subAgentFocus = 0

	m.handleSubAgentPanelKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.subAgents["agent_1"].expanded {
		t.Error("expected expanded after Enter")
	}

	m.handleSubAgentPanelKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.subAgents["agent_1"].expanded {
		t.Error("expected collapsed after second Enter")
	}
}

func TestSubAgentPanel_FocusNavigation(t *testing.T) {
	m := newPanelModel()
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Kind: "started"})
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_2", Kind: "started"})
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_3", Kind: "started"})
	m.subAgentFocus = 2 // start at the bottom

	m.handleSubAgentPanelKey(tea.KeyMsg{Type: tea.KeyUp})
	if m.subAgentFocus != 1 {
		t.Errorf("focus = %d, want 1", m.subAgentFocus)
	}

	m.handleSubAgentPanelKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.subAgentFocus != 2 {
		t.Errorf("focus = %d, want 2", m.subAgentFocus)
	}

	// ↓ past the last agent exits focus mode.
	m.handleSubAgentPanelKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.subAgentFocus != -1 {
		t.Errorf("focus = %d, want -1 (exited)", m.subAgentFocus)
	}
}

func TestSubAgentPanel_RemoveAdjustsFocus(t *testing.T) {
	m := newPanelModel()
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Kind: "started"})
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_2", Kind: "started"})
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_3", Kind: "started"})
	m.subAgentFocus = 1 // focus on agent_2

	m.removeSubAgent("agent_2")
	if m.subAgentFocus != 1 {
		t.Errorf("focus = %d, want 1 (agent_3 slid down)", m.subAgentFocus)
	}

	m.removeSubAgent("agent_3")
	if m.subAgentFocus != 0 {
		t.Errorf("focus = %d, want 0 (last remaining)", m.subAgentFocus)
	}

	m.removeSubAgent("agent_1")
	if m.subAgentFocus != -1 {
		t.Errorf("focus = %d, want -1 (none left)", m.subAgentFocus)
	}
}

func TestSubAgentPanel_LiveHeightExpanded(t *testing.T) {
	m := newPanelModel()
	m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Kind: "started"})
	for i := 0; i < 5; i++ {
		m.handleSubAgentEvent(tools.SubAgentEvent{AgentID: "agent_1", Kind: "tool", ToolName: "t"})
	}
	m.subAgents["agent_1"].expanded = true

	// liveHeight includes textarea + status bar even on an empty model.
	// We only care that expansion adds len(history) extra lines.
	collapsedH := m.liveHeight()
	m.subAgents["agent_1"].expanded = false
	if got := m.liveHeight(); got != collapsedH-5 {
		t.Errorf("collapsed liveHeight = %d, want %d (diff should be 5 history lines)", got, collapsedH-5)
	}
}
