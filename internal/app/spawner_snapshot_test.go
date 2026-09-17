package app

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// twoRoundSender answers the first round with prose and a clean stop, then
// spins on an identical tool_use batch until the stuck detector fires.
type twoRoundSender struct {
	firstReply string
	calls      int32
}

func (s *twoRoundSender) SendMessages(_ context.Context, _, _ string, _ []agent.Message, _ int) (agent.Reply, error) {
	return agent.Reply{Content: "unused"}, nil
}

func (s *twoRoundSender) SendMessagesWithTools(
	_ context.Context, _, _ string, _ []agent.Message, _ int, _ []agent.ToolDefinition,
) (agent.Reply, error) {
	if atomic.AddInt32(&s.calls, 1) == 1 {
		return agent.Reply{Content: s.firstReply, StopReason: "end_turn"}, nil
	}
	return agent.Reply{
		Blocks:     []agent.ContentBlock{agent.NewToolUseBlock("call", "echo_tool", map[string]any{"q": "same"})},
		StopReason: "tool_use",
	}, nil
}

// The carry must not reach back past the round's own prompt: round 1's answer
// was already delivered to the parent, and labelling it as round 2's partial
// work would attribute a finished answer to a run that produced nothing.
func TestAgentSpawner_CarryStopsAtTheRoundBoundary(t *testing.T) {
	const firstAnswer = "Round one answer: the cache key is missing a tenant prefix."
	parent := agent.New(&twoRoundSender{firstReply: firstAnswer}, "parent-model")
	childTools := []agent.ToolDefinition{{Name: "echo_tool"}}
	sp := NewSpawner(parent, echoExecutor{}, func(context.Context) []agent.ToolDefinition { return childTools })

	res, err := sp.Spawn(context.Background(), tools.SpawnRequest{Description: "d", Prompt: "round one"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reply != firstAnswer {
		t.Fatalf("round 1 Reply = %q, want the clean answer", res.Reply)
	}

	res2, err := sp.Continue(context.Background(), res.AgentID, "round two")
	if err != nil {
		t.Fatal(err)
	}
	if res2.StopReason != agent.StopReasonStuck {
		t.Fatalf("round 2 StopReason = %q, want %q", res2.StopReason, agent.StopReasonStuck)
	}
	if strings.Contains(res2.Reply, firstAnswer) {
		t.Errorf("round 2 carried round 1's delivered answer:\n%s", res2.Reply)
	}
	if !strings.HasPrefix(res2.Reply, "[octo] Stopped:") {
		t.Errorf("round 2 should be the bare stop notice, got:\n%s", res2.Reply)
	}
}

// failingSecondRoundSender answers once, then errors.
type failingSecondRoundSender struct{ calls int32 }

func (s *failingSecondRoundSender) SendMessages(_ context.Context, _, _ string, _ []agent.Message, _ int) (agent.Reply, error) {
	if atomic.AddInt32(&s.calls, 1) == 1 {
		return agent.Reply{Content: "first round answer", StopReason: "end_turn"}, nil
	}
	return agent.Reply{}, errors.New("provider exploded")
}

// A failed round must overwrite the snapshot. Leaving the previous round in
// place would have sub_agent_status report a superseded round as the latest
// one — with its reply, as if the child had just said it.
func TestAgentSpawner_FailedRoundReplacesTheSnapshot(t *testing.T) {
	parent := agent.New(&failingSecondRoundSender{}, "parent-model")
	sp := NewSpawner(parent, nilExecutor{}, func(context.Context) []agent.ToolDefinition { return nil })

	res, err := sp.Spawn(context.Background(), tools.SpawnRequest{Description: "d", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Continue(context.Background(), res.AgentID, "again"); err == nil {
		t.Fatal("round 2 should have failed")
	}

	snap, ok := sp.InspectChild(res.AgentID)
	if !ok {
		t.Fatal("child should still be resumable after a failed round")
	}
	if snap.Err == "" {
		t.Error("snapshot should record the failure")
	}
	if snap.Reply != "" || snap.StopReason != "" {
		t.Errorf("failed round should clear the previous round's reply/stop reason, got %+v", snap)
	}
}

// blockingSender holds a round open until released, so a snapshot can be taken
// mid-round.
type blockingSender struct {
	started  chan struct{}
	release  chan struct{}
	oncePast atomic.Bool
}

func (s *blockingSender) SendMessages(_ context.Context, _, _ string, _ []agent.Message, _ int) (agent.Reply, error) {
	if !s.oncePast.Swap(true) {
		close(s.started)
		<-s.release
	}
	return agent.Reply{Content: "done", StopReason: "end_turn"}, nil
}

// A child with a round in flight must not be offered as resumable: the
// registry holds it from before its first round, and a follow-up addressed to
// it would block on the child's lock until that round finishes.
func TestAgentSpawner_RunningChildIsNotResumable(t *testing.T) {
	send := &blockingSender{started: make(chan struct{}), release: make(chan struct{})}
	parent := agent.New(send, "parent-model")
	sp := NewSpawner(parent, nilExecutor{}, func(context.Context) []agent.ToolDefinition { return nil })

	done := make(chan tools.SpawnResult, 1)
	go func() {
		res, err := sp.Spawn(context.Background(), tools.SpawnRequest{Description: "d", Prompt: "p"})
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	<-send.started

	list := sp.ListChildren()
	if len(list) != 1 {
		t.Fatalf("want the running child in the registry, got %d entries", len(list))
	}
	if !list[0].Busy {
		t.Error("a child with a round in flight should be marked Busy")
	}
	snap, ok := sp.InspectChild(list[0].ID)
	if !ok || !snap.Busy {
		t.Errorf("InspectChild should report the running child as Busy, got %+v (found=%v)", snap, ok)
	}

	close(send.release)
	res := <-done
	after, ok := sp.InspectChild(res.AgentID)
	if !ok {
		t.Fatal("child should be resumable once its round finishes")
	}
	if after.Busy {
		t.Error("Busy should clear when the round finishes")
	}
}

// varyingToolSender never repeats a tool_use batch, so it walks the child all
// the way to childMaxTurns instead of tripping the stuck detector.
type varyingToolSender struct {
	withText string
	calls    int32
}

func (s *varyingToolSender) SendMessages(_ context.Context, _, _ string, _ []agent.Message, _ int) (agent.Reply, error) {
	return agent.Reply{Content: "unused"}, nil
}

func (s *varyingToolSender) SendMessagesWithTools(
	_ context.Context, _, _ string, _ []agent.Message, _ int, _ []agent.ToolDefinition,
) (agent.Reply, error) {
	n := atomic.AddInt32(&s.calls, 1)
	var blocks []agent.ContentBlock
	if n == 1 && s.withText != "" {
		blocks = append(blocks, agent.NewTextBlock(s.withText))
	}
	blocks = append(blocks, agent.NewToolUseBlock("call", "echo_tool", map[string]any{"step": int(n)}))
	return agent.Reply{Blocks: blocks, StopReason: "tool_use"}, nil
}

// max_turns is the other half of budgetNoticeOnly and the likelier stop in
// practice, so it gets the same carry as stuck.
func TestAgentSpawner_MaxTurnsCarriesItsWork(t *testing.T) {
	const found = "Halfway there: the handler is registered twice."
	parent := agent.New(&varyingToolSender{withText: found}, "parent-model")
	childTools := []agent.ToolDefinition{{Name: "echo_tool"}}
	sp := NewSpawner(parent, echoExecutor{}, func(context.Context) []agent.ToolDefinition { return childTools })

	res, err := sp.Spawn(context.Background(), tools.SpawnRequest{Description: "d", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != agent.StopReasonMaxTurns {
		t.Fatalf("StopReason = %q, want %q", res.StopReason, agent.StopReasonMaxTurns)
	}
	if !strings.Contains(res.Reply, found) || !strings.Contains(res.Reply, carriedWorkLabel) {
		t.Errorf("max_turns reply should carry the child's labelled work:\n%s", res.Reply)
	}
}
