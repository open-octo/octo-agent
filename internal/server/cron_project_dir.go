package server

import (
	"strings"
	"unicode"
)

// dirNameFor turns a task name into one path segment. Non-ASCII is kept — a
// Chinese or Japanese task name makes a perfectly good directory name — and only
// what a path cannot carry is replaced: separators, the characters Windows
// reserves, and control characters. Leading dots go too, so a task named ".ssh"
// cannot produce a hidden directory.
func dirNameFor(taskName string) string {
	var b strings.Builder
	for _, r := range taskName {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			b.WriteRune('-')
		case unicode.IsControl(r):
			// dropped
		default:
			b.WriteRune(r)
		}
	}
	name := strings.TrimSpace(b.String())
	name = strings.Trim(name, ".-")
	// Windows also refuses a trailing space or dot on any path component.
	name = strings.TrimRight(name, " .")
	if len(name) > 64 {
		name = strings.TrimSpace(name[:64])
	}
	return name
}
