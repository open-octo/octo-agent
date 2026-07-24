package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
)

// forkCaptureSpawner implements ForkSnapshotter and lets a test mutate its
// "parent history" between the tool call returning and the background Spawn
// goroutine running, to prove the fork seed was captured synchronously.
type forkCaptureSpawner struct {
	mu      sync.Mutex
	history []agent.Message

	hold    chan struct{} // Spawn blocks here until the test releases it
	spawned chan SpawnRequest
}

func (s *forkCaptureSpawner) ForkSnapshot() []agent.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]agent.Message(nil), s.history...)
}

func (s *forkCaptureSpawner) setHistory(msgs ...agent.Message) {
	s.mu.Lock()
	s.history = msgs
	s.mu.Unlock()
}

func (s *forkCaptureSpawner) Spawn(_ context.Context, req SpawnRequest) (SpawnResult, error) {
	<-s.hold
	s.spawned <- req
	return SpawnResult{AgentID: "c1", Reply: "ok"}, nil
}

func (s *forkCaptureSpawner) Continue(_ context.Context, _, _ string) (SpawnResult, error) {
	return SpawnResult{}, nil
}

// TestAgentTool_ForkSeedCapturedAtCallTime verifies the fork seed is captured
// on the tool-execution path, not inside the background Spawn goroutine: the
// parent turn keeps running after Start returns, and a late snapshot would
// seed the child with the parent's own "waiting for sub-agents" follow-ups
// (the session-1136b387 bug).
func TestAgentTool_ForkSeedCapturedAtCallTime(t *testing.T) {
	sp := &forkCaptureSpawner{hold: make(chan struct{}), spawned: make(chan SpawnRequest, 1)}
	sp.setHistory(agent.NewUserMessage("original question"))
	mgr := NewSubAgentManager(sp)
	ctx := WithSubAgentManager(context.Background(), mgr)

	res, err := (AgentTool{}).Execute(ctx, "sub_agent", map[string]any{
		"description": "d", "prompt": "explore the trash module", "run_in_background": true,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Text, "Started sub-agent") {
		t.Fatalf("background start should return immediately, got: %q", res.Text)
	}

	// The parent turn moves on before the spawn goroutine gets to run.
	sp.setHistory(
		agent.NewUserMessage("original question"),
		agent.NewAssistantMessage("waiting for the sub-agents to finish"),
	)
	close(sp.hold)

	select {
	case req := <-sp.spawned:
		if !req.ForkConversation {
			t.Fatal("omitted subagent_type should fork")
		}
		if len(req.ForkHistory) != 1 || req.ForkHistory[0].Content != "original question" {
			t.Fatalf("fork seed should be the call-time snapshot, got: %+v", req.ForkHistory)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("background spawn never ran")
	}
}

// TestAgentTool_FreshAgentSkipsForkSnapshot verifies a typed (fresh) child
// doesn't get a conversation seed even when the spawner could provide one.
func TestAgentTool_FreshAgentSkipsForkSnapshot(t *testing.T) {
	sp := &forkCaptureSpawner{hold: make(chan struct{}), spawned: make(chan SpawnRequest, 1)}
	sp.setHistory(agent.NewUserMessage("original question"))
	close(sp.hold)
	mgr := NewSubAgentManager(sp)
	ctx := WithSubAgentManager(context.Background(), mgr)

	if _, err := (AgentTool{}).Execute(ctx, "sub_agent", map[string]any{
		"description": "d", "prompt": "p", "subagent_type": "explore", "run_in_background": true,
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}

	select {
	case req := <-sp.spawned:
		if req.ForkConversation || req.ForkHistory != nil {
			t.Fatalf("typed child must not carry a fork seed: fork=%v history=%+v", req.ForkConversation, req.ForkHistory)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("background spawn never ran")
	}
}
