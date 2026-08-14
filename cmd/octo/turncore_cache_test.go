package main

import "testing"

// TestTurnStatsCacheSuffix locks the summary line's cache tail: the exact
// ", cache NN%" format when the backend reported cache activity, and the
// empty string (cache-less providers keep the old two-part line) when it
// did not.
func TestTurnStatsCacheSuffix(t *testing.T) {
	cases := []struct {
		name  string
		stats TurnStats
		want  string
	}{
		{"no cache info", TurnStats{InputTokens: 1000}, ""},
		{"zero everything", TurnStats{}, ""},
		{"warming turn reports 0%", TurnStats{InputTokens: 100, CacheWriteTokens: 2000}, ", cache 0%"},
		{"typical hit", TurnStats{InputTokens: 114, CacheReadTokens: 2304}, ", cache 95%"},
		{"fully cached", TurnStats{CacheReadTokens: 500}, ", cache 100%"},
	}
	for _, c := range cases {
		if got := c.stats.cacheSuffix(); got != c.want {
			t.Errorf("%s: cacheSuffix() = %q, want %q", c.name, got, c.want)
		}
	}
}
