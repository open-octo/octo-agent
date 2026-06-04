package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
)

func TestTUI_TickAdvancesOnlyWhileRunning(t *testing.T) {
	m := newTestModel()

	m.turnRunning = true
	before := m.spinnerFrame
	_, cmd := m.Update(tickMsg{})
	if m.spinnerFrame != before+1 {
		t.Errorf("spinnerFrame = %d, want %d", m.spinnerFrame, before+1)
	}
	if cmd == nil {
		t.Error("tick should reschedule itself while a turn runs")
	}

	m.turnRunning = false
	m.spinnerFrame = 7
	_, cmd = m.Update(tickMsg{})
	if m.spinnerFrame != 7 {
		t.Errorf("idle tick should not advance the frame; got %d", m.spinnerFrame)
	}
	if cmd != nil {
		t.Error("tick should stop rescheduling once the turn ends")
	}
}

func TestTUI_RunningToolIndicator(t *testing.T) {
	m := newTestModel() // cfg.plain == false → terminal renders as a card

	m.handleEvent(agent.AgentEvent{
		Kind: agent.EventToolStarted, ToolID: "c1", ToolName: "terminal",
		Input: map[string]any{"command": "ls"},
	})
	if m.running == nil {
		t.Fatal("a running card tool should set the live indicator")
	}
	if m.running.verb != "Run" || m.running.target != "ls" {
		t.Errorf("running = %+v, want verb=Run target=ls", *m.running)
	}

	m.handleEvent(agent.AgentEvent{Kind: agent.EventToolDone, ToolID: "c1", ToolName: "terminal", Output: "file"})
	if m.running != nil {
		t.Error("done should clear the live indicator (the card replaces it)")
	}
}

func TestTUI_NonCardToolNoIndicator(t *testing.T) {
	m := newTestModel()
	m.handleEvent(agent.AgentEvent{
		Kind: agent.EventToolStarted, ToolID: "c1", ToolName: "Agent",
		Input: map[string]any{},
	})
	if m.running != nil {
		t.Error("non-card tools commit a started line; they must not set the live indicator")
	}
}

func TestSpinnerLine_Contents(t *testing.T) {
	m := newTestModel()
	out := m.spinnerLine("Run(ls)", time.Now())
	if !strings.Contains(out, "Run(ls)") || !strings.Contains(out, "0s") {
		t.Errorf("spinnerLine = %q, want it to contain the label and elapsed", out)
	}
}

// TestTUI_SpinnerShownWhileWaitingBetweenSteps guards the gap that left the
// TUI frozen with no indicator: after a tool finished and the turn is still
// running, the agent is waiting on the model's next response — there is no
// live tool and no streaming text. The activity line must still show a
// thinking/working spinner so the user can tell the turn isn't idle.
func TestTUI_SpinnerShownWhileWaitingBetweenSteps(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.turnRunning = true
	m.turnStart = time.Now()
	m.spinnerFrame = 0 // thinkingPhrase() == "Thinking"

	// A tool ran and completed earlier this turn (this also latches the old
	// "streaming" flag true), then the agent loops back to the model.
	m.handleEvent(agent.AgentEvent{
		Kind: agent.EventToolStarted, ToolID: "c1", ToolName: "terminal",
		Input: map[string]any{"command": "ls"},
	})
	m.handleEvent(agent.AgentEvent{
		Kind: agent.EventToolDone, ToolID: "c1", ToolName: "terminal", Output: "file",
	})

	// Precondition: the silent inter-step wait — no live tool, no partial text.
	if m.running != nil {
		t.Fatalf("precondition: running should be nil after tool done")
	}
	if m.partial.Len() != 0 {
		t.Fatalf("precondition: partial should be empty, got %q", m.partial.String())
	}

	if out := m.View(); !strings.Contains(out, "Thinking") {
		t.Errorf("expected a thinking spinner while waiting on the next model step; View was:\n%s", out)
	}
}

// TestTUI_NoThinkingSpinnerWhileStreamingText is the other half of the
// contract: while assistant text is actively streaming (partial non-empty),
// the live text is the feedback, so the thinking spinner stays out of the way.
func TestTUI_NoThinkingSpinnerWhileStreamingText(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.turnRunning = true
	m.turnStart = time.Now()
	m.spinnerFrame = 0 // thinkingPhrase() == "Thinking"
	m.partial.WriteString("hello world")

	if out := m.View(); strings.Contains(out, "Thinking") {
		t.Errorf("thinking spinner should be suppressed while text streams; View was:\n%s", out)
	}
}

func TestThinkingPhrase_Rotates(t *testing.T) {
	m := newTestModel()
	m.spinnerFrame = 0
	if got := m.thinkingPhrase(); got != "Thinking" {
		t.Errorf("frame 0 phrase = %q, want Thinking", got)
	}
	m.spinnerFrame = 16
	if got := m.thinkingPhrase(); got == "Thinking" {
		t.Errorf("phrase should rotate by frame; still %q at frame 16", got)
	}
}
