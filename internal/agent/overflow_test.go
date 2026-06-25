package agent

import (
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
