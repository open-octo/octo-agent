package tools

import (
	"strings"
)

// stringArg reads a string argument from a tool-call input map, defaulting to
// "" when absent or not a string.
func stringArg(input map[string]any, key string) string {
	v, _ := input[key].(string)
	return v
}

// firstLine returns the first non-empty line of s, trimmed and capped at 80
// characters — handy for deriving a short label from a longer block.
// stringSliceArg pulls an []string argument tolerating absence and the JSON
// pattern where everything comes through as []any.
func stringSliceArg(input map[string]any, key string) []string {
	raw, ok := input[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// pluralize returns singular when n == 1, otherwise plural.
func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 80 {
			line = strings.TrimSpace(line[:80])
		}
		return line
	}
	return ""
}
