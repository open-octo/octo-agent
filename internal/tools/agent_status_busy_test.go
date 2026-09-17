package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// bareOnlySpawner resumes a child only under its bare id, like the real
// registry does — a prefixed spelling finds nothing.
type bareOnlySpawner struct {
	*scriptedSpawner
	bareID string
}

func (s *bareOnlySpawner) Continue(ctx context.Context, id, msg string) (SpawnResult, error) {
	if id != s.bareID {
		return SpawnResult{}, fmt.Errorf("agent %s is no longer alive (idle-expired or evicted)", id)
	}
	return s.scriptedSpawner.Continue(ctx, id, msg)
}

// A child with a round in flight is not resumable — a follow-up would queue
// behind that round. It must not appear in the resumable listing (the manager's
// own listing covers it while it runs), and a direct query must say so.
func TestStatus_RunningChildIsNotOfferedAsResumable(t *testing.T) {
	busy := ChildSnapshot{ID: "aa11bb22", Busy: true, Turns: 3, Idle: time.Second}
	mgr := NewSubAgentManager(&inspectableSpawner{children: []ChildSnapshot{busy}})

	list, err := AgentStatusTool{}.Execute(followupCtx(mgr), "sub_agent_status", map[string]any{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(list.Text, busy.ID) {
		t.Errorf("a running child should not be listed as resumable:\n%s", list.Text)
	}

	one, err := AgentStatusTool{}.Execute(followupCtx(mgr), "sub_agent_status",
		map[string]any{"agent_id": busy.ID})
	if err != nil {
		t.Fatalf("Execute by id: %v", err)
	}
	if !strings.Contains(one.Text, "working") {
		t.Errorf("a direct query should report the child as working:\n%s", one.Text)
	}
	if strings.Contains(one.Text, "resumable") {
		t.Errorf("a running child must not be advertised as resumable:\n%s", one.Text)
	}
}

// A round that failed outright must not be reported as the previous round's
// clean result.
func TestStatus_FailedRoundIsReportedAsSuch(t *testing.T) {
	failed := ChildSnapshot{ID: "cc33dd44", Err: "provider exploded", Turns: 5, Idle: 2 * time.Second}
	mgr := NewSubAgentManager(&inspectableSpawner{children: []ChildSnapshot{failed}})

	one, err := AgentStatusTool{}.Execute(followupCtx(mgr), "sub_agent_status",
		map[string]any{"agent_id": failed.ID})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(one.Text, "failed") || !strings.Contains(one.Text, "provider exploded") {
		t.Errorf("status should surface the failure:\n%s", one.Text)
	}

	list, err := AgentStatusTool{}.Execute(followupCtx(mgr), "sub_agent_status", map[string]any{})
	if err != nil {
		t.Fatalf("Execute list: %v", err)
	}
	if !strings.Contains(list.Text, "failed") {
		t.Errorf("listing should surface the failure:\n%s", list.Text)
	}
}

// Both sections in one answer: the manager's tracked agents, then the
// spawner's resumable children.
func TestStatus_ListShowsBothSections(t *testing.T) {
	child := stuckChild()
	sp := &inspectableSpawner{children: []ChildSnapshot{child}}
	sp.scriptedSpawner.continueReply = SpawnResult{Reply: "x"}
	mgr := NewSubAgentManager(sp)

	notes := make(chan SubAgentNotification, 1)
	mgr.SetOnExit(func(n SubAgentNotification) { notes <- n })
	id, err := mgr.Start(SpawnRequest{Description: "async work", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	<-notes

	res, err := AgentStatusTool{}.Execute(followupCtx(mgr), "sub_agent_status", map[string]any{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Text, id) {
		t.Errorf("listing should keep the async agent:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, child.ID) {
		t.Errorf("listing should append the resumable child:\n%s", res.Text)
	}
}

// The same id-mangling sub_agent_status tolerates has to work on the send
// side, or the model's follow-up fails after status handed it a bare id it
// then re-prefixes.
func TestSend_SyncChildTolerantOfAgentPrefix(t *testing.T) {
	sp := &scriptedSpawner{continueReply: SpawnResult{AgentID: "dbb7aa4b", Reply: "picked it up"}}
	mgr := NewSubAgentManager(&bareOnlySpawner{scriptedSpawner: sp, bareID: "dbb7aa4b"})

	res, err := AgentSendTool{}.Execute(followupCtx(mgr), "sub_agent_send",
		map[string]any{"agent_id": "agent_dbb7aa4b", "message": "keep going"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Text, "picked it up") {
		t.Errorf("send should have reached the child:\n%s", res.Text)
	}
}
