package agent

import (
	"context"
	"errors"
	"testing"
)

func TestParseOverflowTokens(t *testing.T) {
	cases := []struct {
		name      string
		msg       string
		have, max int
		ok        bool
	}{
		{
			"anthropic", "prompt is too long: 218849 tokens > 200000 maximum",
			218849, 200000, true,
		},
		{
			"openai", "This model's maximum context length is 128000 tokens. However, you requested 130512 tokens",
			130512, 128000, true,
		},
		{
			"not an overflow error", "some unrelated failure",
			0, 0, false,
		},
		{
			"have not greater than max is rejected", "5 tokens > 9 maximum",
			0, 0, false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			have, max, ok := parseOverflowTokens(errors.New(c.msg))
			if ok != c.ok || (ok && (have != c.have || max != c.max)) {
				t.Errorf("parseOverflowTokens(%q) = (%d, %d, %v), want (%d, %d, %v)",
					c.msg, have, max, ok, c.have, c.max, c.ok)
			}
		})
	}
}

// dummyOverflowTools gives Run/RunStream enough reason to enter runLoop rather
// than falling back to the plain Turn path.
var dummyOverflowTools = []ToolDefinition{{Name: "bash", Description: "shell"}}

// TestOverflow_ResetAtStartOfRun verifies that runLoop resets the per-turn
// overflow recovery state before starting a new turn. Without this reset, a
// previous run that set attempted=true (e.g. because recovery itself failed or
// the run errored out after recovery started) would permanently disable
// overflow recovery for the agent.
func TestOverflow_ResetAtStartOfRun(t *testing.T) {
	send := &fakeToolSender{
		replies: []Reply{{Content: "ok", StopReason: "end_turn"}},
	}
	a := New(send, "m")
	a.overflow.attempted = true

	if _, err := a.Run(context.Background(), "hello", dummyOverflowTools, &fakeExecutor{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if a.overflow.attempted {
		t.Error("overflow.attempted was not reset at the start of the run")
	}
}

// TestOverflow_RetriesAfterFailedRun verifies that a run which fails with a
// context-too-long error leaves attempted=true, and that the *next* run resets
// it so recovery can be attempted again if needed.
func TestOverflow_RetriesAfterFailedRun(t *testing.T) {
	send := &fakeToolSender{
		errs: []error{errors.New("prompt is too long: 99999 tokens > 200000 maximum")},
	}
	a := New(send, "m")

	if _, err := a.Run(context.Background(), "hello", dummyOverflowTools, &fakeExecutor{}); err == nil {
		t.Fatal("expected error from context-too-long")
	}
	if !a.overflow.attempted {
		t.Error("overflow.attempted should be true after a recovery attempt")
	}

	// A subsequent run must be able to attempt recovery again.
	send.errs = nil
	send.replies = []Reply{{Content: "ok", StopReason: "end_turn"}}
	send.calls = 0
	if _, err := a.Run(context.Background(), "hello again", dummyOverflowTools, &fakeExecutor{}); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if a.overflow.attempted {
		t.Error("overflow.attempted was not reset on the subsequent run")
	}
}
