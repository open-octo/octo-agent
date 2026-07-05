package channel

import (
	"regexp"
	"strings"
)

// tableSeparatorRe matches a GFM table's header-separator row: two or more
// "---", ":---", "---:", or ":---:" cells joined by "|", with optional
// leading/trailing pipes. Requiring at least one internal "|" (i.e. at least
// two columns) keeps this from ever matching a plain "---" horizontal rule,
// which has no pipe at all.
var tableSeparatorRe = regexp.MustCompile(`^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)+\|?\s*$`)

// FlattenPipeTables converts Markdown pipe tables into plain, readable lines
// for platforms with no table rendering (Weixin, WeCom — #1119). The
// separator row ("| --- | --- |") is dropped as pure clutter, and each
// remaining row's cells are trimmed and rejoined with " | " so uneven source
// padding doesn't carry through. Without this, a table arrives as raw
// "| a | b |" lines with no visual structure at all.
//
// A line only counts as a table row if it's immediately followed by a real
// separator row, so ordinary prose that happens to contain a "|" character
// is left untouched.
func FlattenPipeTables(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))

	for i := 0; i < len(lines); {
		if strings.Contains(lines[i], "|") && i+1 < len(lines) && tableSeparatorRe.MatchString(lines[i+1]) {
			out = append(out, flattenTableRow(lines[i]))
			i += 2 // drop the separator row
			for i < len(lines) && strings.Contains(lines[i], "|") {
				out = append(out, flattenTableRow(lines[i]))
				i++
			}
			continue
		}
		out = append(out, lines[i])
		i++
	}
	return strings.Join(out, "\n")
}

// LooksLikeMarkdownTable reports whether text contains a genuine GFM pipe
// table: a row containing "|" immediately followed by a valid header-
// separator row (#1119). A naive "does any line contain two pipes" check
// (the previous approach) false-positives on ordinary prose that happens to
// use "|" — e.g. as a delimiter in a sentence — flipping rendering modes for
// text that isn't a table at all.
func LooksLikeMarkdownTable(text string) bool {
	lines := strings.Split(text, "\n")
	for i := 0; i+1 < len(lines); i++ {
		if strings.Contains(lines[i], "|") && tableSeparatorRe.MatchString(lines[i+1]) {
			return true
		}
	}
	return false
}

// flattenTableRow strips a row's leading/trailing pipe and rejoins its cells
// (trimmed) with " | ".
func flattenTableRow(line string) string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	cells := strings.Split(trimmed, "|")
	for i, c := range cells {
		cells[i] = strings.TrimSpace(c)
	}
	return strings.Join(cells, " | ")
}
