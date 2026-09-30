package agent

import (
	"context"
	"sync"
	"testing"
)

// upstreamRecorder records the upstream session ID each provider call sees.
type upstreamRecorder struct {
	mu  sync.Mutex
	got []string
}

func (s *upstreamRecorder) SendMessages(ctx context.Context, _, _ string, _ []Message, _ int) (Reply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, UpstreamSessionIDFrom(ctx))
	return Reply{Content: "a title"}, nil
}

// Calls made on a fresh ctx (titles, manual compaction, memory consolidation)
// still carry the agent's ID, and it wins over one already in ctx.
func TestAgent_EntryPointsStampUpstreamSessionID(t *testing.T) {
	send := &upstreamRecorder{}
	a := New(send, "m")
	a.UpstreamSessionID = "sess-1"
	a.History.Append(NewUserMessage("hello"))

	if _, err := a.GenerateTitle(context.Background()); err != nil {
		t.Fatalf("GenerateTitle: %v", err)
	}
	if _, err := a.ConsolidateMemory(context.Background(), "", "note"); err != nil {
		t.Fatalf("ConsolidateMemory: %v", err)
	}
	if _, err := a.Run(WithUpstreamSessionID(context.Background(), "other"), "hi", nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for i, id := range send.got {
		if id != "sess-1" {
			t.Errorf("call %d upstream session ID = %q, want sess-1", i, id)
		}
	}
	if len(send.got) != 3 {
		t.Errorf("got %d provider calls, want 3", len(send.got))
	}
}

func TestAgent_NoUpstreamSessionIDLeavesCtx(t *testing.T) {
	send := &upstreamRecorder{}
	a := New(send, "m")
	a.History.Append(NewUserMessage("hello"))

	if _, err := a.GenerateTitle(WithUpstreamSessionID(context.Background(), "from-ctx")); err != nil {
		t.Fatalf("GenerateTitle: %v", err)
	}
	if len(send.got) != 1 || send.got[0] != "from-ctx" {
		t.Errorf("upstream session IDs = %q, want [from-ctx]", send.got)
	}
}
