package tools

import (
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
)

// inspectableSpawner is a scriptedSpawner that also keeps its children
// resumable, like the real app.Spawner does.
type inspectableSpawner struct {
	scriptedSpawner
	children []ChildSnapshot
}

func (s *inspectableSpawner) InspectChild(id string) (ChildSnapshot, bool) {
	for _, c := range s.children {
		if c.ID == id {
			return c, true
		}
	}
	return ChildSnapshot{}, false
}

func (s *inspectableSpawner) ListChildren() []ChildSnapshot { return s.children }

func stuckChild() ChildSnapshot {
	return ChildSnapshot{
		ID:         "dbb7aa4b",
		StopReason: agent.StopReasonStuck,
		Reply:      "Reproduced the flake on the 3rd run.\n\n[octo] Stopped: detected repeated tool calls without progress.",
		Turns:      12,
		Idle:       90 * time.Second,
	}
}

// A synchronous sub-agent is reaped from the manager as soon as its blocking
// tool call returns, so sub_agent_status has to reach into the spawner's live
// registry — reporting "unknown id" would tell the model to give up on a child
// it could still resume.
func TestStatus_SyncChildIsReportedAsResumable(t *testing.T) {
	child := stuckChild()
	mgr := NewSubAgentManager(&inspectableSpawner{children: []ChildSnapshot{child}})

	res, err := AgentStatusTool{}.Execute(followupCtx(mgr), "sub_agent_status",
		map[string]any{"agent_id": child.ID})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{child.ID, "resumable", "sub_agent_send", "loop detector", "Reproduced the flake"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("status text missing %q:\n%s", want, res.Text)
		}
	}
}

// The reply tag reads "[agent dbb7aa4b]", which models routinely retype as the
// async "agent_N" form. Accept that spelling instead of failing the lookup.
func TestStatus_SyncChildTolerantOfAgentPrefix(t *testing.T) {
	child := stuckChild()
	mgr := NewSubAgentManager(&inspectableSpawner{children: []ChildSnapshot{child}})

	res, err := AgentStatusTool{}.Execute(followupCtx(mgr), "sub_agent_status",
		map[string]any{"agent_id": "agent_" + child.ID})
	if err != nil {
		t.Fatalf("Execute with agent_-prefixed id: %v", err)
	}
	// The resume instruction must name the id sub_agent_send actually accepts,
	// not the mangled one it was asked with.
	if strings.Contains(res.Text, "agent_"+child.ID) {
		t.Errorf("status should echo the bare id, not the prefixed one:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, child.ID) {
		t.Errorf("status text missing the child id:\n%s", res.Text)
	}
}

func TestStatus_UnknownIDDoesNotBlameSyncDispatch(t *testing.T) {
	mgr := NewSubAgentManager(&inspectableSpawner{children: []ChildSnapshot{stuckChild()}})

	_, err := AgentStatusTool{}.Execute(followupCtx(mgr), "sub_agent_status",
		map[string]any{"agent_id": "nope"})
	if err == nil {
		t.Fatal("unknown id should still be an error")
	}
	if strings.Contains(err.Error(), "only async sub-agents are tracked") {
		t.Errorf("error still claims sync agents are untracked: %v", err)
	}
	if !strings.Contains(err.Error(), "resumable") {
		t.Errorf("error should say no resumable child matched either: %v", err)
	}
}

// A spawner that keeps no children (test fakes, and any future spawner without
// a registry) must not break the tool — it just has nothing extra to report.
func TestStatus_SpawnerWithoutInspectorStillErrors(t *testing.T) {
	mgr := NewSubAgentManager(&scriptedSpawner{})

	_, err := AgentStatusTool{}.Execute(followupCtx(mgr), "sub_agent_status",
		map[string]any{"agent_id": "dbb7aa4b"})
	if err == nil {
		t.Fatal("want an error when the spawner keeps no children")
	}
}

func TestStatus_ListIncludesResumableSyncChildren(t *testing.T) {
	child := stuckChild()
	mgr := NewSubAgentManager(&inspectableSpawner{children: []ChildSnapshot{child}})

	res, err := AgentStatusTool{}.Execute(followupCtx(mgr), "sub_agent_status", map[string]any{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Text, child.ID) || !strings.Contains(res.Text, "resumable") {
		t.Errorf("listing should mention the resumable sync child:\n%s", res.Text)
	}
}

// An async spawn's child sits in BOTH the manager and the spawner registry.
// It must be listed once, under its agent_N handle.
func TestStatus_ListDoesNotDoubleCountAsyncChildren(t *testing.T) {
	sp := &inspectableSpawner{}
	sp.scriptedSpawner.continueReply = SpawnResult{Reply: "x"}
	mgr := NewSubAgentManager(sp)

	notes := make(chan SubAgentNotification, 1)
	mgr.SetOnExit(func(n SubAgentNotification) { notes <- n })
	id, err := mgr.Start(SpawnRequest{Description: "async work", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	<-notes

	// scriptedSpawner.Spawn returns AgentID "child_1": that is the spawner-side
	// backing id the manager now holds for agent_1.
	sp.children = []ChildSnapshot{{ID: "child_1", StopReason: "end_turn", Turns: 3}}

	res, err := AgentStatusTool{}.Execute(followupCtx(mgr), "sub_agent_status", map[string]any{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Text, id) {
		t.Errorf("listing should report the async agent as %s:\n%s", id, res.Text)
	}
	if strings.Contains(res.Text, "child_1") {
		t.Errorf("async child listed twice — once under its backing id:\n%s", res.Text)
	}
}

func TestIncompleteNote(t *testing.T) {
	cases := []struct {
		name       string
		stopReason string
		agentID    string
		want       []string
		wantEmpty  bool
	}{
		{name: "clean stop", stopReason: "end_turn", agentID: "abc", wantEmpty: true},
		{
			name: "stuck", stopReason: agent.StopReasonStuck, agentID: "dbb7aa4b",
			want: []string{"INCOMPLETE", "repeating the same tool calls", "sub_agent_send", "dbb7aa4b", "DIFFERENT approach"},
		},
		{
			name: "max turns", stopReason: agent.StopReasonMaxTurns, agentID: "dbb7aa4b",
			want: []string{"INCOMPLETE", "turn limit", "sub_agent_send", "dbb7aa4b"},
		},
		{
			// No id means the spawner kept nothing to resume.
			name: "stuck without an id", stopReason: agent.StopReasonStuck,
			want: []string{"INCOMPLETE", "Re-launch"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := incompleteNote(tc.stopReason, tc.agentID)
			if tc.wantEmpty {
				if got != "" {
					t.Fatalf("want no note for a clean stop, got %q", got)
				}
				return
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("note missing %q: %s", w, got)
				}
			}
			if tc.agentID == "" && strings.Contains(got, "sub_agent_send") {
				t.Errorf("note offers a resume with no id to resume: %s", got)
			}
		})
	}
}
