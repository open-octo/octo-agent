package tools

import "bytes"

// esc is the byte that opens every ANSI escape sequence.
const esc = 0x1b

// stripANSI removes ANSI escape sequences from one line of captured output,
// returning line unchanged when there are none.
//
// Child processes colour their output far more eagerly than "is stdout a
// terminal?" would suggest — vitest's colour gate, for one, only checks that
// $TERM is set and not "dumb", never isatty — and a shell command inherits
// octo's environment, so a piped `npx vitest` still emits SGR codes. A real
// terminal interprets them; a tool card renders the bytes it is given, where
// ESC has no glyph and the rest of the sequence shows up as literal `[31m`.
// Nothing downstream re-emits this output to a terminal, so the sequences are
// dropped here rather than being carried into the tool result (where they also
// cost the model tokens to read past).
func stripANSI(line []byte) []byte {
	if bytes.IndexByte(line, esc) < 0 {
		return line
	}
	out := make([]byte, 0, len(line))
	for i := 0; i < len(line); {
		if line[i] != esc {
			out = append(out, line[i])
			i++
			continue
		}
		i = skipEscape(line, i)
	}
	return out
}

// skipEscape returns the index just past the escape sequence that starts at i,
// where line[i] is ESC. An unterminated sequence swallows the rest of the line,
// which is also what a terminal does with it.
func skipEscape(line []byte, i int) int {
	j := i + 1
	if j >= len(line) {
		return len(line)
	}
	switch line[j] {
	case '[': // CSI: parameter bytes, intermediate bytes, one final byte.
		j++
		for ; j < len(line) && line[j] >= 0x30 && line[j] <= 0x3f; j++ {
		}
		for ; j < len(line) && line[j] >= 0x20 && line[j] <= 0x2f; j++ {
		}
		if j < len(line) {
			j++
		}
		return j
	case ']', 'P', 'X', '^', '_': // OSC/DCS/SOS/PM/APC: a string run, ended by BEL or ST.
		for j++; j < len(line); j++ {
			if line[j] == 0x07 {
				return j + 1
			}
			if line[j] == esc && j+1 < len(line) && line[j+1] == '\\' {
				return j + 2
			}
		}
		return len(line)
	default: // Two- or three-byte escape: intermediate bytes, one final byte.
		for ; j < len(line) && line[j] >= 0x20 && line[j] <= 0x2f; j++ {
		}
		if j < len(line) {
			j++
		}
		return j
	}
}
