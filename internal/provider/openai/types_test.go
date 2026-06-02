package openai

import "testing"

func TestApiUsage_CachedTokens(t *testing.T) {
	// DeepSeek: explicit hit count.
	if got := (apiUsage{PromptCacheHitTokens: 128}).cachedTokens(); got != 128 {
		t.Errorf("deepseek hit = %d, want 128", got)
	}
	// OpenAI: nested prompt_tokens_details.cached_tokens.
	u := apiUsage{}
	u.PromptTokensDetails = &struct {
		CachedTokens int `json:"cached_tokens"`
	}{CachedTokens: 64}
	if got := u.cachedTokens(); got != 64 {
		t.Errorf("openai cached = %d, want 64", got)
	}
	// Neither reported → 0.
	if got := (apiUsage{}).cachedTokens(); got != 0 {
		t.Errorf("no cache info = %d, want 0", got)
	}
	// DeepSeek hit takes precedence when both somehow present.
	u2 := apiUsage{PromptCacheHitTokens: 200}
	u2.PromptTokensDetails = &struct {
		CachedTokens int `json:"cached_tokens"`
	}{CachedTokens: 1}
	if got := u2.cachedTokens(); got != 200 {
		t.Errorf("precedence = %d, want 200", got)
	}
}

func TestApiUsage_NonCachedInput(t *testing.T) {
	// Real DeepSeek warm-cache shape: prompt_tokens is the WHOLE input,
	// prompt_cache_hit_tokens a subset. InputTokens must exclude the cached
	// part so it doesn't overlap CacheReadTokens (hit 2688 + miss 20 = 2708).
	ds := apiUsage{PromptTokens: 2708, PromptCacheHitTokens: 2688, PromptCacheMissTokens: 20}
	if got := ds.nonCachedInput(); got != 20 {
		t.Errorf("deepseek nonCachedInput = %d, want 20", got)
	}
	if got := ds.cachedTokens(); got != 2688 {
		t.Errorf("deepseek cachedTokens = %d, want 2688", got)
	}
	// Cold turn: no cache, full prompt is uncached.
	if got := (apiUsage{PromptTokens: 2707}).nonCachedInput(); got != 2707 {
		t.Errorf("cold nonCachedInput = %d, want 2707", got)
	}
	// Defensive: cached > prompt clamps to 0 rather than going negative.
	if got := (apiUsage{PromptTokens: 10, PromptCacheHitTokens: 99}).nonCachedInput(); got != 0 {
		t.Errorf("clamp nonCachedInput = %d, want 0", got)
	}
}
