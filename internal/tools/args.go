package tools

import "strings"

// stringArg reads a string argument from a tool-call input map, defaulting to
// "" when absent or not a string.
func stringArg(input map[string]any, key string) string {
	v, _ := input[key].(string)
	return v
}

// firstLine returns the first non-empty line of s, trimmed and capped at 80
// characters — handy for deriving a short label from a longer block.
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
