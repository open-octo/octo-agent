package app

import (
	"context"
	"sync"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// sessionCtxSender records the upstream session ID each provider call sees.
type sessionCtxSender struct {
	mu       sync.Mutex
	upstream []string
}

func (s *sessionCtxSender) SendMessages(ctx context.Context, _, _ string, _ []agent.Message, _ int) (agent.Reply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upstream = append(s.upstream, agent.UpstreamSessionIDFrom(ctx))
	return agent.Reply{Content: "ok"}, nil
}

// SubAgentManager hands Spawn a detached ctx and a workflow hands it the
// parent's turn ctx; either way the child reports its own ID, stable across
// Continue.
func TestSpawner_ChildUsesOwnUpstreamSessionID(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"detached ctx":      context.Background(),
		"parent's turn ctx": agent.WithUpstreamSessionID(context.Background(), "parent-sess"),
	} {
		t.Run(name, func(t *testing.T) {
			send := &sessionCtxSender{}
			parent := agent.New(send, "m")
			parent.UpstreamSessionID = "parent-sess"
			sp := NewSpawner(parent, nilExecutor{}, func(context.Context) []agent.ToolDefinition { return nil })

			res, err := sp.Spawn(ctx, tools.SpawnRequest{Description: "d", Prompt: "p"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := sp.Continue(ctx, res.AgentID, "again"); err != nil {
				t.Fatal(err)
			}
			want := "parent-sess/agent-" + res.AgentID
			if len(send.upstream) != 2 || send.upstream[0] != want || send.upstream[1] != want {
				t.Errorf("child upstream session IDs = %q, want both %q", send.upstream, want)
			}
		})
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
