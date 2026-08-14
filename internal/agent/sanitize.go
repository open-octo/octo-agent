package agent

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// ansiEscape matches well-formed ANSI escape sequences: CSI sequences
// (colors, cursor movement — ESC [ params final-byte) and OSC sequences
// (window titles, hyperlinks — ESC ] payload terminated by BEL or ST).
var ansiEscape = regexp.MustCompile(`\x1b(?:\[[0-9;:?<=>]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\))`)

// sanitizeToolResultText makes tool output safe for the JSON wire and useful
// to the model. Commands that dump binary data (analyzing an executable,
// catting an image) fill the result with NUL and other control bytes and with
// invalid UTF-8 — strict backends reject a literal NUL (\u0000) in JSON
// strings, and Go's json.Marshal silently coerces invalid UTF-8 at encode
// time, leaving the persisted session and the wire disagreeing about what
// the model saw.
//
//   - Well-formed ANSI escape sequences are stripped: they carry styling,
//     not information, and once the ESC byte is replaced below they would
//     degrade into "�[31m" noise.
//   - Remaining C0 control bytes other than \t, \n, \r — plus DEL — are
//     replaced with U+FFFD. Correctly-encoded C1 controls (U+0080–U+009F)
//     are deliberately left alone: they are valid UTF-8 and harmless on the
//     JSON wire, while a raw C1 byte is invalid UTF-8 and caught below.
//   - Invalid UTF-8 is coerced to U+FFFD here, eagerly, so history matches
//     the bytes actually sent.
//
// The common case (clean text) is detected with a single byte scan and
// returned unchanged.
func sanitizeToolResultText(s string) string {
	if !needsSanitize(s) {
		return s
	}
	if strings.IndexByte(s, 0x1b) >= 0 {
		s = ansiEscape.ReplaceAllString(s, "")
	}
	s = strings.ToValidUTF8(s, "�")
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return '�'
		}
		return r
	}, s)
}

// needsSanitize reports whether s contains a control byte (other than \t, \n,
// \r), DEL, or invalid UTF-8. A pure byte scan: control bytes and UTF-8
// validity are both decidable without decoding runes.
func needsSanitize(s string) bool {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < 0x20 && b != '\t' && b != '\n' && b != '\r' {
			return true
		}
		if b == 0x7f {
			return true
		}
	}
	return !utf8.ValidString(s)
}
