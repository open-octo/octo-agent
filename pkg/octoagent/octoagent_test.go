package octoagent

import (
	"context"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

// stubSender is the minimal agent.Sender implementation for tests.
type stubSender struct{}

func (stubSender) SendMessages(ctx context.Context, model, system string, messages []Message, maxTokens int) (Reply, error) {
	return Reply{}, nil
}

func TestAliasTypesAreIdentical(t *testing.T) {
	// If these compile and run, the aliases successfully bridge the internal/agent
	// types out to pkg/octoagent without introducing new types.
	var a *Agent = New(stubSender{}, "stub-model")
	if a == nil {
		t.Fatal("New returned nil")
	}
	if a.History == nil {
		t.Fatal("New did not initialize History")
	}
	var _ *History = a.History // compile-time alias check (pointer, never copy)

	// Construct a message using aliased constants and constructors.
	msg := NewUserMessage("hello")
	if msg.Role != RoleUser {
		t.Fatalf("expected RoleUser, got %q", msg.Role)
	}
	if msg.Content != "hello" {
		t.Fatalf("expected content %q, got %q", "hello", msg.Content)
	}

	replyMsg := NewAssistantMessage("hi")
	if replyMsg.Role != RoleAssistant {
		t.Fatalf("expected RoleAssistant, got %q", replyMsg.Role)
	}

	// Stop reason constants are reachable.
	var _ = StopReasonMaxTurns
	var _ = StopReasonInterrupted
	var _ = StopReasonMaxTokens
	var _ = StopReasonStuck

	// Event kinds are reachable.
	var _ EventKind = EventTextDelta
	var _ EventKind = EventTurnDone

	// Sender-capability interfaces are aliased (compile-time check): an internal
	// value must satisfy the pkg-level interface without conversion. Guards
	// against a new agent.*Sender capability being added without an SDK alias.
	var _ func(agent.LowEffortSender) LowEffortSender = func(s agent.LowEffortSender) LowEffortSender { return s }
	var _ func(agent.NoReasoningSender) NoReasoningSender = func(s agent.NoReasoningSender) NoReasoningSender { return s }

	// Goal status constants are reachable.
	var _ GoalStatus = GoalActive

	// ContentBlock constructors are reachable.
	tb := NewTextBlock("text")
	if tb.Type != "text" {
		t.Fatalf("expected text block, got %q", tb.Type)
	}
}

func TestAgentMatchesInternalAgent(t *testing.T) {
	// This asserts that octoagent.Agent is exactly agent.Agent, so values can
	// be passed between the two without conversion.
	var internal *agent.Agent = New(stubSender{}, "stub-model")
	if internal == nil {
		t.Fatal("assignment to *agent.Agent failed")
	}
}
