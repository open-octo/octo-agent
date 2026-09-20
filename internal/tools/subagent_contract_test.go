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
	mgr.SetSynchronous(true) // inline dispatch: the annotation rides the tool_result
	ctx := WithSubAgentManager(context.Background(), mgr)

	res, err := (AgentTool{}).Execute(ctx, "sub_agent", map[string]any{
		"description": "d", "prompt": "p", "subagent_type": "general",
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
	mgr2.SetSynchronous(true)
	ctx2 := WithSubAgentManager(context.Background(), mgr2)
	res2, _ := (AgentTool{}).Execute(ctx2, "sub_agent", map[string]any{"description": "d", "prompt": "p", "subagent_type": "general"})
	if strings.Contains(res2.Text, "INCOMPLETE") {
		t.Errorf("a complete result should not be flagged, got: %q", res2.Text)
	}
}

// TestAgentTool_RequiresSubagentType verifies omitting subagent_type is a
// hard error that names the available presets — every sub-agent is a fresh,
// typed agent; conversation branching is the session branch feature's job.
func TestAgentTool_RequiresSubagentType(t *testing.T) {
	mgr := NewSubAgentManager(&resultSpawner{reply: "done"})
	ctx := WithSubAgentManager(context.Background(), mgr)

	_, err := (AgentTool{}).Execute(ctx, "sub_agent", map[string]any{
		"description": "d", "prompt": "p",
	})
	if err == nil {
		t.Fatal("omitting subagent_type should be an error")
	}
	if !strings.Contains(err.Error(), "subagent_type is required") {
		t.Errorf("error should say what is missing, got: %v", err)
	}
	if !strings.Contains(err.Error(), "explore") {
		t.Errorf("error should list available presets, got: %v", err)
	}
}

// TestAgentTool_NoBackgroundParameter locks the parameter out of the schema.
// The model used to pick sync vs async itself and reliably guessed "short",
// so a minutes-long child pinned the parent turn and the user could not get a
// word in until it finished.
func TestAgentTool_NoBackgroundParameter(t *testing.T) {
	def := (AgentTool{}).Definition()
	props, ok := def.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties map: %#v", def.Parameters)
	}
	if _, exposed := props["run_in_background"]; exposed {
		t.Error("sub_agent must not expose run_in_background — dispatch belongs to the transport")
	}
	if strings.Contains(def.Description, "run_in_background") {
		t.Errorf("the description must not name the removed parameter: %q", def.Description)
	}
}

// TestAgentTool_DispatchFollowsTransport is the rule that replaced the
// parameter: a transport that can deliver a completion notification
// backgrounds every child so the parent turn ends immediately; one that
// cannot runs it inline and hands back the reply, because a background
// result would have nowhere to land.
func TestAgentTool_DispatchFollowsTransport(t *testing.T) {
	args := map[string]any{"description": "d", "prompt": "p", "subagent_type": "general"}

	t.Run("no follow-up channel runs inline", func(t *testing.T) {
		mgr := NewSubAgentManager(&resultSpawner{reply: "child result"})
		mgr.SetSynchronous(true)
		res, err := (AgentTool{}).Execute(WithSubAgentManager(context.Background(), mgr), "sub_agent", args)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(res.Text, "child result") {
			t.Errorf("inline dispatch should return the child's reply, got: %q", res.Text)
		}
		if strings.Contains(res.Text, "Started sub-agent") {
			t.Errorf("inline dispatch must not hand back an async stub, got: %q", res.Text)
		}
	})

	t.Run("follow-up channel runs in the background", func(t *testing.T) {
		mgr := NewSubAgentManager(&resultSpawner{reply: "child result"})
		res, err := (AgentTool{}).Execute(WithSubAgentManager(context.Background(), mgr), "sub_agent", args)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(res.Text, "Started sub-agent") {
			t.Errorf("background dispatch should return the handle, got: %q", res.Text)
		}
		if strings.Contains(res.Text, "child result") {
			t.Errorf("background dispatch must not block for the reply, got: %q", res.Text)
		}
	})
}
