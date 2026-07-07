package main

import (
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
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

// startTicker is the single choke point that prevents a busy background
// workflow/subagent — which fires many events between turns — from spawning
// a parallel tickMsg->tickCmd chain per event. Before this guard existed,
// every such event unconditionally called tickCmd(), and since tea.Tick
// always starts a brand-new independent timer, N events produced N live
// chains all incrementing spinnerFrame every tickInterval — the spinner
// visibly spun several times faster than intended the busier a workflow was.
func TestTUI_StartTicker_NoDoubleChain(t *testing.T) {
	m := newTestModel()

	cmd1 := m.startTicker()
	if cmd1 == nil {
		t.Fatal("first startTicker call should start the chain")
	}
	if !m.tickerActive {
		t.Fatal("tickerActive should be true once a chain has started")
	}

	// A second event arriving while the chain from the first is still alive
	// (e.g. another workflowEventMsg before the next tickMsg fires) must not
	// spawn a parallel chain.
	if cmd2 := m.startTicker(); cmd2 != nil {
		t.Error("startTicker should be a no-op while a chain is already active")
	}

	// Once the chain has actually stopped (tickMsg's idle branch clears
	// tickerActive), a later event can start a fresh one again.
	m.tickerActive = false
	if cmd3 := m.startTicker(); cmd3 == nil {
		t.Error("startTicker should restart the chain once the previous one has stopped")
	}
}

// TestTUI_WorkflowEventsWhileIdleStartTickerOnce is the integration-level
// counterpart to TestTUI_StartTicker_NoDoubleChain: it drives the real
// workflowEventMsg/subAgentEventMsg branches of Update (not startTicker
// directly) to confirm they're actually wired through the guard, matching a
// real busy background workflow flooding the TUI with progress events while
// no foreground turn is running.
func TestTUI_WorkflowEventsWhileIdleStartTickerOnce(t *testing.T) {
	m := newTestModel()
	m.turnRunning = false

	ev := tools.WorkflowEvent{RunID: "wf_1", Kind: "progress", Line: "step 1"}
	if _, cmd := m.Update(workflowEventMsg{ev: ev}); cmd == nil {
		t.Fatal("first workflow event while idle should start the ticker")
	}
	if !m.tickerActive {
		t.Fatal("tickerActive should be true after the first workflow event")
	}

	// Flood more progress events, as a busy workflow would, without letting a
	// tickMsg run in between (turnRunning stays false throughout, mirroring
	// idle time between turns). None of these may restart the chain — capture
	// each returned cmd (rather than discarding it) so a regression back to
	// unconditional tickCmd() calls would actually be observed here, not just
	// via the separate direct startTicker() check below.
	for i := 0; i < 10; i++ {
		_, cmd := m.Update(workflowEventMsg{ev: tools.WorkflowEvent{RunID: "wf_1", Kind: "progress", Line: "step"}})
		if cmd != nil {
			t.Fatalf("flood iteration %d: workflow event while ticker already active returned a non-nil cmd (a duplicate tick chain)", i)
		}
	}
	if got := m.startTicker(); got != nil {
		t.Error("flooding workflow events while idle spawned more than one live tick chain")
	}

	// A subagent event is guarded the same way and shares the same flag.
	m.tickerActive = false
	sub := tools.SubAgentEvent{AgentID: "agent_1", Kind: "started"}
	if _, cmd := m.Update(subAgentEventMsg{ev: sub}); cmd == nil {
		t.Fatal("first subagent event while idle should start the ticker")
	}
	if got := m.startTicker(); got != nil {
		t.Error("a subagent event while the ticker is already active should not restart it")
	}
}

// TestTUI_TickerRestartsAfterIdleViaRealUpdate drives the full round trip
// through real Update() dispatch — unlike TestTUI_StartTicker_NoDoubleChain
// and TestTUI_WorkflowEventsWhileIdleStartTickerOnce, which set
// m.tickerActive=false directly to simulate "the chain has stopped". Here a
// genuine idle tickMsg (the same message the running tea.Tick would deliver)
// must clear the flag via the tickMsg case's own idle branch, and a
// follow-up event must then be able to restart the chain — so the idle
// condition in the tickMsg case and the guard in startTicker can't silently
// drift out of sync with each other.
func TestTUI_TickerRestartsAfterIdleViaRealUpdate(t *testing.T) {
	m := newTestModel()
	m.turnRunning = false

	// Starts the chain. Kind "progress" for a run that was never "started"
	// leaves m.workflows empty (handleWorkflowEvent only updates an existing
	// entry on "progress"), so it doesn't itself trip tickMsg's idle check below.
	if _, cmd := m.Update(workflowEventMsg{ev: tools.WorkflowEvent{RunID: "wf_1", Kind: "progress", Line: "x"}}); cmd == nil {
		t.Fatal("workflow event while idle should start the ticker")
	}
	if !m.tickerActive {
		t.Fatal("tickerActive should be true after starting")
	}

	// A real tickMsg, with everything else idle, must hit the idle branch and
	// clear the flag — not just stop rescheduling.
	if _, cmd := m.Update(tickMsg{}); cmd != nil {
		t.Fatal("idle tickMsg should not reschedule")
	}
	if m.tickerActive {
		t.Fatal("idle tickMsg should clear tickerActive so a later event can restart the chain")
	}

	// A follow-up event must be able to restart the chain now that the
	// previous one has actually stopped.
	if _, cmd := m.Update(workflowEventMsg{ev: tools.WorkflowEvent{RunID: "wf_1", Kind: "progress", Line: "y"}}); cmd == nil {
		t.Error("a later workflow event should restart the ticker once the previous chain has stopped")
	}
	if !m.tickerActive {
		t.Error("tickerActive should be true again after the restart")
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

func TestTUI_NonCardToolIndicator(t *testing.T) {
	m := newTestModel()
	m.handleEvent(agent.AgentEvent{
		Kind: agent.EventToolStarted, ToolID: "c1", ToolName: "sub_agent",
		Input: map[string]any{},
	})
	if m.running == nil || m.running.verb != "sub_agent" {
		t.Errorf("non-card tools suppress their started line and show the live indicator instead; got %+v", m.running)
	}
	if len(m.printlnBuf) != 0 {
		t.Errorf("started must not commit a scrollback line; got %v", m.printlnBuf)
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
