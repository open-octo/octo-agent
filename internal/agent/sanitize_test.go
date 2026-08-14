package agent

import (
	"strings"
	"testing"
)

func TestSanitizeToolResultText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"clean text unchanged", "hello 世界\nsecond line\ttabbed\r\n", "hello 世界\nsecond line\ttabbed\r\n"},
		{"nul replaced", "MZ\x00\x00PE header", "MZ��PE header"},
		{"c0 controls replaced", "a\x01b\x02c", "a�b�c"},
		{"del replaced", "a\x7fb", "a�b"},
		{"tab newline cr kept", "a\tb\nc\rd", "a\tb\nc\rd"},
		{"csi color stripped", "\x1b[31mred\x1b[0m plain", "red plain"},
		{"csi cursor stripped", "\x1b[2Kline", "line"},
		{"csi with intermediate byte stripped", "\x1b[4 qcursor", "cursor"},
		// Correctly-encoded C1 controls are valid UTF-8 and pass through.
		{"encoded c1 kept", "a\u0085b", "a\u0085b"},
		{"osc bel stripped", "\x1b]0;title\x07body", "body"},
		{"osc st stripped", "\x1b]8;;https://x\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"bare esc replaced", "a\x1bb", "a�b"},
		{"unterminated osc leaves replaced esc", "a\x1b]0;title", "a�]0;title"},
		// A run of invalid bytes collapses to ONE replacement char —
		// strings.ToValidUTF8 semantics, and less noise for the model.
		{"invalid utf8 coerced", "ok\xff\xfebad", "ok�bad"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeToolResultText(c.in); got != c.want {
				t.Errorf("sanitizeToolResultText(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestToolResultBlocks_SanitizesBinaryGarbage pins the integration point: a
// tool result carrying binary bytes must enter history sanitized, on both the
// success and the error path.
func TestToolResultBlocks_SanitizesBinaryGarbage(t *testing.T) {
	blocks := toolResultBlocks("tu_1", ToolResult{Text: "ELF\x00\x00\x01\x02"}, nil)
	if got := blocks[0].Result; strings.ContainsRune(got, 0) {
		t.Errorf("success path kept a NUL byte: %q", got)
	}

	blocks = toolResultBlocks("tu_2", ToolResult{}, errBinary("fail\x00ure"))
	if got := blocks[0].Result; strings.ContainsRune(got, 0) {
		t.Errorf("error path kept a NUL byte: %q", got)
	}
}

type errBinary string

func (e errBinary) Error() string { return string(e) }

// TestSanitizeThenCompact_CapHolds ensures sanitization runs before the size
// backstop: replacing single junk bytes with the 3-byte U+FFFD must not push
// the stored result past ToolResultMaxBytes.
func TestSanitizeThenCompact_CapHolds(t *testing.T) {
	junk := strings.Repeat("x\x01", ToolResultMaxBytes) // expands ~2x when sanitized
	blocks := toolResultBlocks("tu_3", ToolResult{Text: junk}, nil)
	if n := len(blocks[0].Result); n > ToolResultMaxBytes {
		t.Errorf("stored result is %d bytes, want <= %d", n, ToolResultMaxBytes)
	}
}
