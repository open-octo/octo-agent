package anthropic

import "testing"

// Modern Claude models must use adaptive thinking + output_config.effort and
// must NOT send budget_tokens (which 400s on Opus 4.7+). The budget value
// becomes a max_tokens floor instead.
func TestApplyReasoning_AdaptiveClaude(t *testing.T) {
	b := apiRequest{MaxTokens: 4096}
	applyReasoning(&b, "claude-opus-4-7", "high", 32768)

	if b.Thinking == nil || b.Thinking.Type != "adaptive" {
		t.Fatalf("Thinking = %+v, want type=adaptive", b.Thinking)
	}
	if b.Thinking.BudgetTokens != 0 {
		t.Errorf("BudgetTokens = %d, want 0 (must not send budget_tokens on adaptive)", b.Thinking.BudgetTokens)
	}
	if b.OutputConfig == nil || b.OutputConfig.Effort != "high" {
		t.Errorf("OutputConfig = %+v, want effort=high", b.OutputConfig)
	}
	if b.MaxTokens != 32768+DefaultMaxTokens {
		t.Errorf("MaxTokens = %d, want budget floor %d", b.MaxTokens, 32768+DefaultMaxTokens)
	}
}

// "xhigh" was introduced with Opus 4.7, so it must clamp to "high" on Opus 4.6
// / Sonnet 4.6 (which would reject it) and pass through on Opus 4.7+.
func TestApplyReasoning_XHighClamp(t *testing.T) {
	for _, tc := range []struct {
		model, want string
	}{
		{"claude-opus-4-7", "xhigh"},
		{"claude-opus-4-8", "xhigh"},
		{"claude-opus-4-6", "high"},
		{"claude-sonnet-4-6", "high"},
	} {
		b := apiRequest{MaxTokens: 200000}
		applyReasoning(&b, tc.model, "xhigh", 48000)
		if b.OutputConfig == nil || b.OutputConfig.Effort != tc.want {
			t.Errorf("%s xhigh → %+v, want effort=%q", tc.model, b.OutputConfig, tc.want)
		}
	}
}

// Older Claude (Haiku 4.5) and non-Claude Anthropic-protocol backends (Kimi for
// coding) keep the legacy thinking.budget_tokens path — no output_config.
func TestApplyReasoning_BudgetPath(t *testing.T) {
	for _, model := range []string{"kimi-for-coding", "claude-haiku-4-5"} {
		b := apiRequest{MaxTokens: 100}
		applyReasoning(&b, model, "high", 1024)
		if b.Thinking == nil || b.Thinking.Type != "enabled" || b.Thinking.BudgetTokens != 1024 {
			t.Errorf("%s: Thinking = %+v, want enabled/1024", model, b.Thinking)
		}
		if b.OutputConfig != nil {
			t.Errorf("%s: OutputConfig = %+v, want nil on budget path", model, b.OutputConfig)
		}
		if b.MaxTokens != 1024+DefaultMaxTokens {
			t.Errorf("%s: MaxTokens = %d, want bumped above budget", model, b.MaxTokens)
		}
	}
}

// Effort off (empty) sends no thinking or effort on any model.
func TestApplyReasoning_Off(t *testing.T) {
	for _, model := range []string{"claude-opus-4-7", "kimi-for-coding"} {
		b := apiRequest{MaxTokens: 4096}
		applyReasoning(&b, model, "", 0)
		if b.Thinking != nil || b.OutputConfig != nil {
			t.Errorf("%s off: Thinking=%+v OutputConfig=%+v, want both nil", model, b.Thinking, b.OutputConfig)
		}
	}
}
