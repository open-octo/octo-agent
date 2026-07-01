package hooks

import (
	"context"
	"testing"
)

func preToolEngine(t *testing.T, body, matcher string) *Engine {
	t.Helper()
	e := NewEngine(nil)
	if err := e.RegisterShellMatched(EventPreToolUse, makeScript(t, body), matcher, false, 0); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestPreToolUse_Exit2Blocks(t *testing.T) {
	e := preToolEngine(t, "echo 'rm is forbidden' >&2; exit 2", "")
	dec := e.PreToolUse(context.Background(), Payload{Event: EventPreToolUse, ToolName: "terminal"})
	if !dec.Block {
		t.Fatal("exit 2 must block")
	}
	if dec.Reason != "rm is forbidden" {
		t.Errorf("reason should come from stderr: %q", dec.Reason)
	}
}

func TestPreToolUse_Exit0NoOpinion(t *testing.T) {
	e := preToolEngine(t, "exit 0", "")
	dec := e.PreToolUse(context.Background(), Payload{Event: EventPreToolUse, ToolName: "terminal"})
	if dec.Block || dec.Allow {
		t.Errorf("silent exit 0 should be no-opinion, got %+v", dec)
	}
}

func TestPreToolUse_StructuredApprove(t *testing.T) {
	e := preToolEngine(t, `echo '{"decision":"approve"}'`, "")
	dec := e.PreToolUse(context.Background(), Payload{Event: EventPreToolUse, ToolName: "terminal"})
	if !dec.Allow || dec.Block {
		t.Errorf("decision approve should allow, got %+v", dec)
	}
}

func TestPreToolUse_StructuredBlockWithReason(t *testing.T) {
	e := preToolEngine(t, `echo '{"decision":"block","reason":"policy X"}'`, "")
	dec := e.PreToolUse(context.Background(), Payload{Event: EventPreToolUse, ToolName: "terminal"})
	if !dec.Block || dec.Reason != "policy X" {
		t.Errorf("decision block should carry reason, got %+v", dec)
	}
}

func TestPreToolUse_MatcherGates(t *testing.T) {
	e := preToolEngine(t, "exit 2", "terminal")
	// Non-matching tool → hook doesn't run → no opinion.
	if dec := e.PreToolUse(context.Background(), Payload{Event: EventPreToolUse, ToolName: "read_file"}); dec.Block {
		t.Error("matcher should skip read_file")
	}
	// Matching tool → blocks.
	if dec := e.PreToolUse(context.Background(), Payload{Event: EventPreToolUse, ToolName: "terminal"}); !dec.Block {
		t.Error("matcher should apply to terminal")
	}
}

func TestPreToolUse_BlockWinsOverAllow(t *testing.T) {
	e := NewEngine(nil)
	// First hook approves, second blocks — block must win.
	if err := e.RegisterShellMatched(EventPreToolUse, makeScript(t, `echo '{"decision":"approve"}'`), "", false, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.RegisterShellMatched(EventPreToolUse, makeScript(t, "exit 2"), "", false, 0); err != nil {
		t.Fatal(err)
	}
	dec := e.PreToolUse(context.Background(), Payload{Event: EventPreToolUse, ToolName: "terminal"})
	if !dec.Block {
		t.Errorf("a later block must override an earlier approve, got %+v", dec)
	}
}

func TestPreToolUse_OtherExitIsNonBlocking(t *testing.T) {
	var notices int
	e := preToolEngine(t, "echo boom >&2; exit 1", "")
	e.Notify = func(string) { notices++ }
	dec := e.PreToolUse(context.Background(), Payload{Event: EventPreToolUse, ToolName: "terminal"})
	if dec.Block || dec.Allow {
		t.Errorf("exit 1 (not 2) must be non-blocking, got %+v", dec)
	}
	if notices == 0 {
		t.Error("a non-blocking hook error should surface via Notify")
	}
}

func TestPreToolUse_NoHooksNoOpinion(t *testing.T) {
	e := NewEngine(nil)
	if dec := e.PreToolUse(context.Background(), Payload{Event: EventPreToolUse, ToolName: "terminal"}); dec.Block || dec.Allow {
		t.Errorf("unconfigured PreToolUse must be no-opinion, got %+v", dec)
	}
}
