package main

import (
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/tools"
)

// newWorkflowPanelModel builds a tuiModel with just the workflow panel state.
func newWorkflowPanelModel() *tuiModel {
	return &tuiModel{workflows: map[string]*workflowUI{}}
}

func TestWorkflowPanel_StartedProgressDone(t *testing.T) {
	m := newWorkflowPanelModel()
	m.handleWorkflowEvent(tools.WorkflowEvent{RunID: "wf_1", Description: "Audit modules", Kind: "started"})
	if len(m.workflows) != 1 {
		t.Fatalf("workflows = %d, want 1", len(m.workflows))
	}
	wf := m.workflows["wf_1"]
	if wf == nil {
		t.Fatal("wf_1 missing")
	}
	if wf.description != "Audit modules" {
		t.Errorf("description = %q, want Audit modules", wf.description)
	}
	if wf.status != "running" {
		t.Errorf("status = %q, want running", wf.status)
	}

	m.handleWorkflowEvent(tools.WorkflowEvent{RunID: "wf_1", Kind: "progress", Line: "agent auth started"})
	if wf.lastLine != "agent auth started" {
		t.Errorf("lastLine = %q, want 'agent auth started'", wf.lastLine)
	}

	m.handleWorkflowEvent(tools.WorkflowEvent{RunID: "wf_1", Kind: "done", Status: "done"})
	if len(m.workflows) != 0 {
		t.Errorf("workflows = %d after done, want 0", len(m.workflows))
	}
}

func TestWorkflowPanel_MultipleOrdering(t *testing.T) {
	m := newWorkflowPanelModel()
	m.handleWorkflowEvent(tools.WorkflowEvent{RunID: "wf_2", Description: "Second", Kind: "started"})
	m.handleWorkflowEvent(tools.WorkflowEvent{RunID: "wf_1", Description: "First", Kind: "started"})
	// Force distinct timestamps so ordering is deterministic regardless of
	// time.Now() resolution on this platform.
	m.workflows["wf_2"].start = m.workflows["wf_2"].start.Add(-time.Second)

	order := m.workflowOrder()
	want := []string{"wf_2", "wf_1"}
	if len(order) != len(want) || order[0] != want[0] || order[1] != want[1] {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestWorkflowPanel_TieBreakerNumeric(t *testing.T) {
	m := newWorkflowPanelModel()
	now := time.Now()
	m.workflows["wf_10"] = &workflowUI{description: "Ten", start: now}
	m.workflows["wf_2"] = &workflowUI{description: "Two", start: now}

	order := m.workflowOrder()
	want := []string{"wf_2", "wf_10"}
	if len(order) != len(want) || order[0] != want[0] || order[1] != want[1] {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestWorkflowPanel_RenderRendersRunning(t *testing.T) {
	m := newWorkflowPanelModel()
	m.width = 120
	m.height = 40
	m.handleWorkflowEvent(tools.WorkflowEvent{RunID: "wf_1", Description: "Audit modules", Kind: "started"})
	m.handleWorkflowEvent(tools.WorkflowEvent{RunID: "wf_1", Kind: "progress", Line: "agent auth started"})

	view := m.renderWorkflowsPanel()
	if !strings.Contains(view, "workflows (1 running)") {
		t.Errorf("render missing workflow panel title:\n%s", view)
	}
	if !strings.Contains(view, "Audit modules") {
		t.Errorf("render missing workflow description:\n%s", view)
	}
	if !strings.Contains(view, "agent auth started") {
		t.Errorf("render missing progress line:\n%s", view)
	}
}

func TestWorkflowPanel_RemovesOnDone(t *testing.T) {
	m := newWorkflowPanelModel()
	m.handleWorkflowEvent(tools.WorkflowEvent{RunID: "wf_1", Kind: "started"})
	m.handleWorkflowEvent(tools.WorkflowEvent{RunID: "wf_1", Kind: "done", Status: "error"})
	if _, ok := m.workflows["wf_1"]; ok {
		t.Error("wf_1 still present after done event")
	}
}
