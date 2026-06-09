package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

// blockingSpawner blocks in Spawn until release is closed, so a test can hold
// async sub-agents "running" and exercise the concurrency cap.
type blockingSpawner struct{ release chan struct{} }

func (s *blockingSpawner) Spawn(_ context.Context, _ SpawnRequest) (SpawnResult, error) {
	<-s.release
	return SpawnResult{AgentID: "c", Reply: "done"}, nil
}
func (s *blockingSpawner) Continue(_ context.Context, _, _ string) (SpawnResult, error) {
	return SpawnResult{}, nil
}

// resultSpawner returns a fixed reply + stop reason synchronously.
type resultSpawner struct {
	reply      string
	stopReason string
}

func (s *resultSpawner) Spawn(_ context.Context, _ SpawnRequest) (SpawnResult, error) {
	return SpawnResult{AgentID: "c1", Reply: s.reply, StopReason: s.stopReason}, nil
}
func (s *resultSpawner) Continue(_ context.Context, _, _ string) (SpawnResult, error) {
	return SpawnResult{}, nil
}

// TestSubAgentManager_ConcurrencyCap verifies async spawns are bounded: the
// cap-th+1 launch is rejected while the others are running, and a slot frees up
// once one finishes.
func TestSubAgentManager_ConcurrencyCap(t *testing.T) {
	sp := &blockingSpawner{release: make(chan struct{})}
	mgr := NewSubAgentManager(sp) // async

	for i := 0; i < maxConcurrentSubAgents; i++ {
		if _, err := mgr.Start(SpawnRequest{Description: "x"}); err != nil {
			t.Fatalf("start %d (within cap) should succeed: %v", i, err)
		}
	}
	if _, err := mgr.Start(SpawnRequest{Description: "over"}); err == nil {
		t.Fatal("start past the cap should be rejected")
	} else if !strings.Contains(err.Error(), "too many") {
		t.Errorf("cap error should explain itself, got: %v", err)
	}

	// Let the running ones finish; a slot should free up.
	close(sp.release)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := mgr.Start(SpawnRequest{Description: "after"}); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("a slot should free up after running sub-agents finish")
}

// TestAgentTool_SyncMaxTurnsSurfaced verifies a sync sub-agent that hit its
// turn limit is flagged INCOMPLETE rather than passed off as a finished result.
func TestAgentTool_SyncMaxTurnsSurfaced(t *testing.T) {
	mgr := NewSubAgentManager(&resultSpawner{reply: "partial work", stopReason: "max_turns"})
	ctx := WithSubAgentManager(context.Background(), mgr)

	res, err := (AgentTool{}).Execute(ctx, "sub_agent", map[string]any{
		"description": "d", "prompt": "p",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Text, "INCOMPLETE") {
		t.Errorf("max_turns result should be flagged INCOMPLETE, got: %q", res.Text)
	}
	if !strings.Contains(res.Text, "partial work") {
		t.Errorf("the partial reply should still be returned, got: %q", res.Text)
	}

	// A normal completion must NOT be flagged.
	mgr2 := NewSubAgentManager(&resultSpawner{reply: "done", stopReason: ""})
	ctx2 := WithSubAgentManager(context.Background(), mgr2)
	res2, _ := (AgentTool{}).Execute(ctx2, "sub_agent", map[string]any{"description": "d", "prompt": "p"})
	if strings.Contains(res2.Text, "INCOMPLETE") {
		t.Errorf("a complete result should not be flagged, got: %q", res2.Text)
	}
}

// TestAgentTool_ForcedSyncNote verifies that asking for background on a
// synchronous transport tells the model it ran synchronously instead of
// silently ignoring the choice.
func TestAgentTool_ForcedSyncNote(t *testing.T) {
	mgr := NewSubAgentManager(&resultSpawner{reply: "done"})
	mgr.SetSynchronous(true)
	ctx := WithSubAgentManager(context.Background(), mgr)

	res, err := (AgentTool{}).Execute(ctx, "sub_agent", map[string]any{
		"description": "d", "prompt": "p", "run_in_background": true,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Text, "ran synchronously") {
		t.Errorf("forced-sync downgrade should be surfaced, got: %q", res.Text)
	}
}
