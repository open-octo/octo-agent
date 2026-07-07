package hooks

import (
	"context"
	"testing"

	"github.com/open-octo/octo-agent/pkg/octoagent"
)

// capturingSender records the messages it was asked to send so the test can
// inspect what the hook injected into the turn's user text.
type capturingSender struct {
	lastMessages []octoagent.Message
}

func (s *capturingSender) SendMessages(ctx context.Context, model, system string, messages []octoagent.Message, maxTokens int) (octoagent.Reply, error) {
	s.lastMessages = messages
	return octoagent.Reply{Content: "ok"}, nil
}

func TestEngine_RegisterInProc_InjectsIntoTurn(t *testing.T) {
	engine := NewEngine(nil)
	engine.RegisterInProc(EventUserPromptSubmit, func(ctx context.Context, p Payload) string {
		return "REMINDER"
	})

	sender := &capturingSender{}
	a := octoagent.New(sender, "stub-model")
	a.Hooks = engine
	a.HookMeta = Meta{SessionID: "sess-1", Transport: "test"}

	if _, err := a.Turn(context.Background(), "hello"); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	if len(sender.lastMessages) == 0 {
		t.Fatal("sender received no messages")
	}
	last := sender.lastMessages[len(sender.lastMessages)-1]
	if last.Content == "" || last.Content == "hello" {
		t.Fatalf("expected the hook's injected text ahead of the user input, got %q", last.Content)
	}
	if got := last.Content; got != "REMINDER\n\nhello" {
		t.Errorf("message content = %q, want %q", got, "REMINDER\n\nhello")
	}
}

func TestEngine_PreToolUse_Blocks(t *testing.T) {
	engine := NewEngine(nil)
	engine.RegisterInProc(EventPreToolUse, func(ctx context.Context, p Payload) string {
		return `{"decision":"block","reason":"no tools allowed"}`
	})

	decision := engine.PreToolUse(context.Background(), Payload{
		Event:    EventPreToolUse,
		ToolName: "terminal",
	})
	if !decision.Block {
		t.Errorf("PreToolUse decision = %+v, want Block=true", decision)
	}
	if decision.Reason != "no tools allowed" {
		t.Errorf("PreToolUse reason = %q, want %q", decision.Reason, "no tools allowed")
	}
}

func TestSeenSet_SharedAcrossEngines(t *testing.T) {
	seen := NewSeenSet()
	e1 := NewEngine(seen)
	e2 := NewEngine(seen)

	// persistedStarted=true routes through the "resume" branch, the only one
	// that actually consults the seen-set (persistedStarted=false always
	// fires as "startup" regardless of what's been seen).
	src, fire := e1.SessionStartDecision("sess-shared", true, false)
	if !fire || src != "resume" {
		t.Fatalf("e1 SessionStartDecision: expected fire=true source=resume on first call, got fire=%v source=%q", fire, src)
	}
	if _, fire := e2.SessionStartDecision("sess-shared", true, false); fire {
		t.Error("e2 SessionStartDecision: expected fire=false — SeenSet is shared with e1")
	}
}
