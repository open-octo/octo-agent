package app

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// sessionCtxSender records the ctx session IDs each provider call sees.
type sessionCtxSender struct {
	mu       sync.Mutex
	upstream []string
	toolsSID []string
}

func (s *sessionCtxSender) SendMessages(ctx context.Context, _, _ string, _ []agent.Message, _ int) (agent.Reply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upstream = append(s.upstream, agent.UpstreamSessionIDFrom(ctx))
	s.toolsSID = append(s.toolsSID, tools.SessionIDFrom(ctx))
	return agent.Reply{Content: "ok"}, nil
}

func TestSpawner_ChildUsesOwnUpstreamSessionID(t *testing.T) {
	send := &sessionCtxSender{}
	parent := agent.New(send, "m")
	sp := NewSpawner(parent, nilExecutor{}, func(context.Context) []agent.ToolDefinition { return nil })

	ctx := tools.WithSessionID(context.Background(), "parent-sess")
	res, err := sp.Spawn(ctx, tools.SpawnRequest{Description: "d", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Continue(ctx, res.AgentID, "again"); err != nil {
		t.Fatal(err)
	}

	want := "parent-sess/agent-" + res.AgentID
	if len(send.upstream) != 2 || send.upstream[0] != want || send.upstream[1] != want {
		t.Errorf("child upstream session IDs = %q, want both %q (own ID, stable across Continue)", send.upstream, want)
	}
	// The tools-layer ID stays the parent's so per-session tool state is shared.
	for _, sid := range send.toolsSID {
		if sid != "parent-sess" {
			t.Errorf("child tools session ID = %q, want parent-sess", sid)
		}
	}
	if strings.Contains(agent.UpstreamSessionIDFrom(ctx), "agent-") {
		t.Errorf("parent ctx was mutated: %q", agent.UpstreamSessionIDFrom(ctx))
	}
}

// SubAgentManager hands Spawn a detached ctx, so the parent arrives on the
// request instead.
func TestSpawner_ParentSessionIDFromRequest(t *testing.T) {
	send := &sessionCtxSender{}
	parent := agent.New(send, "m")
	sp := NewSpawner(parent, nilExecutor{}, func(context.Context) []agent.ToolDefinition { return nil })

	res, err := sp.Spawn(context.Background(), tools.SpawnRequest{Description: "d", Prompt: "p", ParentSessionID: "parent-sess"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "parent-sess/agent-" + res.AgentID; len(send.upstream) != 1 || send.upstream[0] != want {
		t.Errorf("child upstream session IDs = %q, want [%q]", send.upstream, want)
	}
}

func TestSpawner_NoParentSessionUsesBareID(t *testing.T) {
	send := &sessionCtxSender{}
	parent := agent.New(send, "m")
	sp := NewSpawner(parent, nilExecutor{}, func(context.Context) []agent.ToolDefinition { return nil })

	res, err := sp.Spawn(context.Background(), tools.SpawnRequest{Description: "d", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(send.upstream) != 1 || send.upstream[0] != res.AgentID {
		t.Errorf("child upstream session IDs = %q, want [%q]", send.upstream, res.AgentID)
	}
}
