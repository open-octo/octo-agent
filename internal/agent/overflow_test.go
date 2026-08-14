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
			"kimi", "Invalid request: Your request exceeded model token limit: 262144 (requested: 269030)",
			269030, 262144, true,
		},
		{
			"not an overflow error", "some unrelated failure",
			0, 0, false,
		},
		{
			"have not greater than max is rejected", "5 tokens > 9 maximum",
			0, 0, false,
		},
		{
			"kimi have not greater than max is rejected", "exceeded model token limit: 262144 (requested: 1000)",
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

func TestContextTooLongError(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{"anthropic", "prompt is too long: 218849 tokens > 200000 maximum", true},
		{"openai", "This model's maximum context length is 128000 tokens", true},
		{"dashscope glm", "InternalError.Algo.InvalidParameter: Range of input length should be [1, 202752]", true},
		{"kimi", "Invalid request: Your request exceeded model token limit: 262144 (requested: 269030)", true},
		{"zhipu 1261", "Prompt 超长", true},
		{"zhipu 1261 no space", "Prompt超长", true},
		{"cn generic length limit", "输入长度超限，请减少输入内容", true},
		{"cn generic context", "上下文长度超过模型限制", true},
		// The Chinese wording of an OUTPUT-side max_tokens rejection must
		// not be classified as context overflow — a bare "长度超限" phrase
		// would match it.
		{"cn output length rejection", "输出长度超限，请降低 max_tokens", false},
		{"unrelated failure", "connection reset by peer", false},
		// A max_tokens PARAMETER rejection is not a context overflow —
		// treating it as one dead-ends recovery in a compression loop.
		{"max_tokens rejection", "max_tokens: expected a value <= 32768, but got 65536 instead", false},
		{"rate limit", "rate limit exceeded, please retry later", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := contextTooLongError(errors.New(c.msg)); got != c.want {
				t.Errorf("contextTooLongError(%q) = %v, want %v", c.msg, got, c.want)
			}
		})
	}
}

// TestComputePullBack_SmallHistoryStaysInBounds guards against a regression
// where clamp(half, 4, historyLen-2) returned an out-of-range value (the
// floor of 4) whenever historyLen-2 fell below it, which made the caller's
// own "k >= len(msgs)-1" guard reject every aggressive pull-back attempt for
// 4-5 message histories — silently disabling Layer 2 overflow recovery
// exactly when the small, single-turn histories that trip it need it most.
func TestComputePullBack_SmallHistoryStaysInBounds(t *testing.T) {
	for historyLen := 4; historyLen <= 10; historyLen++ {
		k := computePullBack(historyLen, pullBackAggressive)
		if k <= 0 || k >= historyLen-1 {
			t.Errorf("computePullBack(%d, aggressive) = %d, want in [1, %d]", historyLen, k, historyLen-2)
		}
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
