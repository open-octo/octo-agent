package app

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// loopingChildSender scripts a child that says something useful, then repeats
// the identical tool_use batch until the agent loop's stuck detector cuts it
// off. withText controls whether the first round carries prose — a child that
// only ever called tools has nothing to carry back.
type loopingChildSender struct {
	withText string
	calls    int32
}

func (s *loopingChildSender) SendMessages(_ context.Context, _, _ string, _ []agent.Message, _ int) (agent.Reply, error) {
	return agent.Reply{Content: "unused"}, nil
}

func (s *loopingChildSender) SendMessagesWithTools(
	_ context.Context, _, _ string, _ []agent.Message, _ int, _ []agent.ToolDefinition,
) (agent.Reply, error) {
	n := atomic.AddInt32(&s.calls, 1)
	blocks := []agent.ContentBlock{}
	if n == 1 && s.withText != "" {
		blocks = append(blocks, agent.NewTextBlock(s.withText))
	}
	// Same tool, same input every round: one fingerprint, so five consecutive
	// batches trip the detector.
	blocks = append(blocks, agent.NewToolUseBlock("call", "echo_tool", map[string]any{"q": "same"}))
	return agent.Reply{Blocks: blocks, StopReason: "tool_use"}, nil
}

func TestAgentSpawner_StuckChildCarriesItsWorkAndStaysResumable(t *testing.T) {
	const found = "The flake is the 5s sub-context in upgrade.Check."
	parent := agent.New(&loopingChildSender{withText: found}, "parent-model")
	childTools := []agent.ToolDefinition{{Name: "echo_tool"}}
	sp := NewSpawner(parent, echoExecutor{}, func(context.Context) []agent.ToolDefinition { return childTools })

	res, err := sp.Spawn(context.Background(), tools.SpawnRequest{Description: "hunt the flake", Prompt: "go"})
	if err != nil {
		t.Fatalf("a stuck child should return a result, not an error: %v", err)
	}
	if res.StopReason != agent.StopReasonStuck {
		t.Fatalf("StopReason = %q, want %q", res.StopReason, agent.StopReasonStuck)
	}
	// The stop notice alone is what the loop puts in the reply; without the
	// carry the parent learns nothing about what the child actually found.
	if !strings.Contains(res.Reply, found) {
		t.Errorf("reply dropped the child's work:\n%s", res.Reply)
	}
	if !strings.Contains(res.Reply, "repeated tool calls") {
		t.Errorf("reply should still carry the stop notice:\n%s", res.Reply)
	}
	// Carried text is labelled, and the label comes before the text it
	// introduces — the parent must not read it as the child's final answer.
	if i, j := strings.Index(res.Reply, carriedWorkLabel), strings.Index(res.Reply, found); i < 0 || i > j {
		t.Errorf("carried text should be labelled up front:\n%s", res.Reply)
	}

	// Same round, seen through the inspector sub_agent_status uses.
	snap, ok := sp.InspectChild(res.AgentID)
	if !ok {
		t.Fatalf("child %s should still be resumable after a stuck stop", res.AgentID)
	}
	if snap.StopReason != agent.StopReasonStuck {
		t.Errorf("snapshot StopReason = %q, want %q", snap.StopReason, agent.StopReasonStuck)
	}
	if snap.Reply != res.Reply {
		t.Errorf("snapshot reply differs from what the parent got:\n%s", snap.Reply)
	}
	if snap.Turns == 0 {
		t.Error("snapshot should report the turns the child burned")
	}

	if list := sp.ListChildren(); len(list) != 1 || list[0].ID != res.AgentID {
		t.Errorf("ListChildren = %+v, want just %s", list, res.AgentID)
	}
	if _, ok := sp.InspectChild("not-an-id"); ok {
		t.Error("InspectChild should not invent children")
	}
}

// Nothing to carry: the notice is all the parent gets, and it must not be
// mangled or duplicated.
func TestAgentSpawner_StuckChildWithNoTextReturnsNoticeOnly(t *testing.T) {
	parent := agent.New(&loopingChildSender{}, "parent-model")
	childTools := []agent.ToolDefinition{{Name: "echo_tool"}}
	sp := NewSpawner(parent, echoExecutor{}, func(context.Context) []agent.ToolDefinition { return childTools })

	res, err := sp.Spawn(context.Background(), tools.SpawnRequest{Description: "spin", Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Reply, "[octo] Stopped:") {
		t.Errorf("want the bare stop notice, got:\n%s", res.Reply)
	}
	if strings.Count(res.Reply, "[octo] Stopped:") != 1 {
		t.Errorf("stop notice repeated:\n%s", res.Reply)
	}
}

// A clean stop must pass through untouched — the carry is for budget stops only.
func TestAgentSpawner_CleanStopIsNotAnnotated(t *testing.T) {
	parent := agent.New(&subAgentSender{reply: "sub-agent answer"}, "parent-model")
	sp := NewSpawner(parent, nilExecutor{}, func(context.Context) []agent.ToolDefinition { return nil })

	res, err := sp.Spawn(context.Background(), tools.SpawnRequest{Description: "d", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reply != "sub-agent answer" {
		t.Errorf("Reply = %q, want it verbatim", res.Reply)
	}
	snap, ok := sp.InspectChild(res.AgentID)
	if !ok {
		t.Fatal("child should be resumable after a clean stop too")
	}
	if snap.Reply != "sub-agent answer" {
		t.Errorf("snapshot Reply = %q", snap.Reply)
	}
}
