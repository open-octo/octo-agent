package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ToolResultMaxBytes is the per-tool-result size backstop. A single tool
// result larger than this (a multi-MB file read, a grep with thousands of
// hits, a chatty build log) is truncated middle-out before it enters history,
// so one pathological call can't dominate the context window. It's a backstop,
// not a tuning knob: the value is generous enough that ordinary large outputs
// pass through untouched.
//
// Truncation happens at production time (in dispatchTools), NOT by rewriting
// old history — so it never mutates already-sent messages and therefore never
// invalidates the conversation prompt cache. The user still sees the full
// output live via streaming tool-progress events; only the copy retained for
// the model is capped.
const ToolResultMaxBytes = 40_000

// microCompact returns s unchanged when it's within the size backstop, else a
// middle-out truncation with a marker noting how much was dropped.
func microCompact(s string) string {
	if len(s) <= ToolResultMaxBytes {
		return s
	}
	return truncateMiddle(s, ToolResultMaxBytes)
}

// truncateMiddle keeps the head and tail of s and replaces the middle with a
// marker, so the total stays near max bytes. Head and tail are trimmed to
// valid UTF-8 rune boundaries so the result is always valid UTF-8 (required by
// the JSON wire format). The tail is often where errors/exit codes land, so
// keeping both ends beats a head-only cut.
func truncateMiddle(s string, max int) string {
	if len(s) <= max {
		return s
	}
	omitted := len(s) - max
	marker := fmt.Sprintf("\n\n... [%d bytes truncated by octo to fit context — re-run with a narrower query/range for the full output] ...\n\n", omitted)

	budget := max - len(marker)
	if budget < 0 {
		budget = 0
	}
	head := budget / 2
	tail := budget - head

	var b strings.Builder
	b.WriteString(trimToValidUTF8Prefix(s[:head]))
	b.WriteString(marker)
	b.WriteString(trimToValidUTF8Suffix(s[len(s)-tail:]))
	return b.String()
}

// trimToValidUTF8Prefix drops a trailing partial rune from a byte-sliced head.
func trimToValidUTF8Prefix(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// trimToValidUTF8Suffix drops a leading partial rune from a byte-sliced tail.
func trimToValidUTF8Suffix(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[1:]
	}
	return s
}
