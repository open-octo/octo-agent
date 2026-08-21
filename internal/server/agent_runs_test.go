package server

import (
	"testing"

	"github.com/open-octo/octo-agent/internal/tools"
)

func TestAgentEventsPathRejectsTraversal(t *testing.T) {
	for _, bad := range []string{"", "..", "../etc", "a/b", "/abs", "x/../y"} {
		if _, err := agentEventsPath(bad); err == nil {
			t.Errorf("agentEventsPath(%q) accepted an unsafe id", bad)
		}
	}
	if _, err := agentEventsPath("normal-session-id"); err != nil {
		t.Errorf("agentEventsPath rejected a normal id: %v", err)
	}
}

// TestAgentRunsRoundTrip appends a realistic event mix through the same
// payload builders the live broadcast uses, then reduces the sidecar and
// checks the trails a reloaded tab would hydrate from.
func TestAgentRunsRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	s := &Server{}
	const sid = "agent-runs-roundtrip"
	for _, ev := range []map[string]any{
		subAgentEventPayload(sid, tools.SubAgentEvent{AgentID: "agent_1", Description: "explore x", AgentType: "explore", Kind: "started"}),
		subAgentEventPayload(sid, tools.SubAgentEvent{AgentID: "agent_1", Kind: "tool", ToolID: "t1", ToolName: "read_file", ToolInput: map[string]any{"path": "go.mod"}}),
		subAgentEventPayload(sid, tools.SubAgentEvent{AgentID: "agent_1", Kind: "text", Text: "interim thought"}),
		subAgentEventPayload(sid, tools.SubAgentEvent{AgentID: "agent_1", Kind: "tool_done", ToolID: "t1", ToolName: "read_file", ToolOutput: "module x"}),
		subAgentEventPayload(sid, tools.SubAgentEvent{AgentID: "agent_1", Kind: "done", StopReason: "end_turn", Result: "final answer"}),
		workflowEventPayload(sid, tools.WorkflowEvent{RunID: "wf_1", Description: "audit", Kind: "started"}),
		workflowEventPayload(sid, tools.WorkflowEvent{RunID: "wf_1", Kind: "progress", Line: "phase: scan"}),
		workflowEventPayload(sid, tools.WorkflowEvent{RunID: "wf_1", Kind: "agent_started", AgentID: "a1", AgentLabel: "check auth"}),
		workflowEventPayload(sid, tools.WorkflowEvent{RunID: "wf_1", Kind: "agent_tool", AgentID: "a1", ToolID: "t9", ToolName: "grep"}),
		workflowEventPayload(sid, tools.WorkflowEvent{RunID: "wf_1", Kind: "agent_tool_done", AgentID: "a1", ToolID: "t9", ToolName: "grep", ToolOutput: "3 hits"}),
		workflowEventPayload(sid, tools.WorkflowEvent{RunID: "wf_1", Kind: "agent_done", AgentID: "a1", Reply: "auth ok"}),
		workflowEventPayload(sid, tools.WorkflowEvent{RunID: "wf_1", Kind: "done", Status: "done"}),
	} {
		s.appendAgentEvent(sid, ev)
	}

	path, err := agentEventsPath(sid)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := reduceAgentEventsFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(resp.SubAgents) != 1 {
		t.Fatalf("sub_agents = %d, want 1", len(resp.SubAgents))
	}
	sa := resp.SubAgents[0]
	if sa.AgentID != "agent_1" || sa.Description != "explore x" || sa.AgentType != "explore" {
		t.Errorf("sub-agent identity = %+v", sa)
	}
	if sa.Status != "done" || sa.StopReason != "end_turn" || sa.Result != "final answer" {
		t.Errorf("sub-agent completion = %+v", sa)
	}
	if len(sa.Steps) != 2 {
		t.Fatalf("sub-agent steps = %+v, want tool+text", sa.Steps)
	}
	if st := sa.Steps[0]; st.Kind != "tool" || st.Name != "read_file" || !st.Done || st.Error || st.Output != "module x" {
		t.Errorf("tool step = %+v", st)
	}
	if input := sa.Steps[0].Input; input["path"] != "go.mod" {
		t.Errorf("tool step input = %v", input)
	}
	if st := sa.Steps[1]; st.Kind != "text" || st.Text != "interim thought" {
		t.Errorf("text step = %+v", st)
	}

	if len(resp.Workflows) != 1 {
		t.Fatalf("workflows = %d, want 1", len(resp.Workflows))
	}
	wf := resp.Workflows[0]
	if wf.RunID != "wf_1" || wf.Description != "audit" || wf.Status != "done" {
		t.Errorf("workflow identity = %+v", wf)
	}
	if len(wf.Logs) != 1 || wf.Logs[0] != "phase: scan" {
		t.Errorf("workflow logs = %v", wf.Logs)
	}
	if len(wf.Agents) != 1 {
		t.Fatalf("workflow agents = %+v, want 1", wf.Agents)
	}
	a := wf.Agents[0]
	if a.AgentID != "a1" || a.Label != "check auth" || a.Status != "done" || a.Reply != "auth ok" {
		t.Errorf("workflow agent = %+v", a)
	}
	if len(a.Steps) != 1 || a.Steps[0].Output != "3 hits" || !a.Steps[0].Done {
		t.Errorf("workflow agent steps = %+v", a.Steps)
	}

	// A missing sidecar reduces to empty lists, not an error.
	gone, err := agentEventsPath("never-ran")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := reduceAgentEventsFile(gone)
	if err != nil {
		t.Fatalf("missing sidecar should not error: %v", err)
	}
	if len(empty.SubAgents) != 0 || len(empty.Workflows) != 0 {
		t.Errorf("missing sidecar reduced to %+v", empty)
	}
}
