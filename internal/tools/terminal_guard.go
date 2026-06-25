package tools

import (
	"fmt"
	"regexp"
	"strings"
)

// sedInPlace matches an invocation of `sed` with an in-place edit flag
// (`-i`, `-i.bak`, `-i ”`, bundled short flags like `-ni`, or the long
// `--in-place`). It deliberately stops at a shell separator (| ; &) so a
// pipeline like `cat x | sed 's/a/b/' > y` — which is NOT an in-place edit —
// doesn't false-positive on a later `sed -i` in a different command.
var sedInPlace = regexp.MustCompile(`\bsed\b[^|;&\n]*\s-(?:-in-place\b|[a-z]*i\b)`)

// guardCommand inspects a shell command for patterns that should be refused
// with an educational message, distinct from the permission engine's broad
// allow/deny/ask policy. The engine decides "may this run at all"; the guard
// catches terminal-specific footguns and steers the model to a better tool.
//
// Currently it only intercepts in-place stream edits (`sed -i` and friends),
// which would otherwise bypass the file tools' permission checks, diff
// rendering, and read-before-write tracking. Returning an error here turns
// into an IsError tool_result, so the model reads the hint and retries with
// edit_file.
func guardCommand(command string) error {
	if sedInPlace.MatchString(maskQuoted(command)) {
		return fmt.Errorf("refusing in-place `sed` edit: use the edit_file tool instead, " +
			"so the change is permission-checked, shown as a diff, and tracked for " +
			"read-before-write")
	}
	return nil
}

// maskQuoted replaces single- and double-quoted substrings with spaces so
// regex-based guards don't false-positive on literal text inside quotes
// (e.g. `echo "sed -i"`). It does not attempt full shell parsing; nested
// quotes and backslash escapes are ignored, which is sufficient for guarding
// against accidental sed flags in quoted arguments.
func maskQuoted(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSingle, inDouble := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case 0x27:
			if !inDouble {
				inSingle = !inSingle
				b.WriteByte(c)
			} else {
				b.WriteByte(' ')
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
				b.WriteByte(c)
			} else {
				b.WriteByte(' ')
			}
		default:
			if inSingle || inDouble {
				b.WriteByte(' ')
			} else {
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}
