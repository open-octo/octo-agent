package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
)

// EditFileTool replaces an exact substring inside an existing file. The
// match must be unique unless replace_all is true. Refuses to create the
// file if it doesn't exist — use write_file for that.
type EditFileTool struct{}

// curly quote constants for normalization
const (
	leftSingleCurlyQuote  = '‘' // '
	rightSingleCurlyQuote = '’' // '
	leftDoubleCurlyQuote  = '“' // "
	rightDoubleCurlyQuote = '”' // "
)

func (EditFileTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "edit_file",
		Description: "Replace an exact substring in an existing file. " +
			"You MUST read the file with read_file before calling this tool. " +
			"old_string must be the file's raw content — never include the line-number " +
			"column (e.g. \"     3\\t\") that read_file prepends for display; that prefix " +
			"is not part of the file and will cause a not-found error. " +
			"old_string must appear exactly once (or set replace_all=true to swap every occurrence). " +
			"The file must already exist — use write_file to create. " +
			"Include enough surrounding context in old_string (typically 2-4 lines) " +
			"to make it unique. If old_string matches multiple times and replace_all is false, " +
			"the edit will fail — add more context to disambiguate.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "File path (absolute preferred).",
				},
				"old_string": map[string]any{
					"type":        "string",
					"description": "Exact text to find. Must appear in the file. Include enough surrounding context for it to be unique unless replace_all is set.",
				},
				"new_string": map[string]any{
					"type":        "string",
					"description": "Replacement text. Empty string is allowed (deletes old_string).",
				},
				"replace_all": map[string]any{
					"type":        "boolean",
					"description": "When true, replace every occurrence instead of requiring a unique match. Defaults to false.",
				},
			},
			"required": []string{"path", "old_string", "new_string"},
		},
	}
}

// normalizeQuotes converts curly quotes to straight quotes, handling
// the common case where LLMs output straight quotes but files contain
// curly quotes (or vice versa).
func normalizeQuotes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case leftSingleCurlyQuote, rightSingleCurlyQuote:
			b.WriteByte('\'')
		case leftDoubleCurlyQuote, rightDoubleCurlyQuote:
			b.WriteByte('"')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// stripTrailingWhitespace removes trailing spaces/tabs from every line while
// preserving line endings. SplitAfter keeps the trailing "\n" on each fragment;
// we peel that newline off before TrimRight and re-attach it, otherwise the
// "\n" (not in the cutset) shields the spaces in front of it and only the final
// line would ever be trimmed. Skipped for markdown files where a trailing
// double-space is a hard line break.
func stripTrailingWhitespace(s string) string {
	lines := strings.SplitAfter(s, "\n")
	for i, line := range lines {
		if nl := strings.HasSuffix(line, "\n"); nl {
			lines[i] = strings.TrimRight(line[:len(line)-1], " \t\r") + "\n"
		} else {
			lines[i] = strings.TrimRight(line, " \t\r")
		}
	}
	return strings.Join(lines, "")
}

// indentWidths are the tab widths tried by the leading-whitespace fallback,
// ordered by likelihood: read_file and the terminal tool render tabs 4 wide,
// 8 is the classic terminal default, 2 is a common compact style models emit.
var indentWidths = []int{4, 8, 2}

// findActualString attempts to locate the search string in the file content,
// first with the fallback chain in findActualStringCore (exact, quote
// normalization, indent normalization, trailing-whitespace normalization),
// and — only if the caller's search string looks like it was copied straight
// out of read_file's "NNNNNN\t" line-number column — retries the whole chain
// with that column stripped. Returns the actual string found in the file
// (preserving original formatting) and the tab width that matched — 4, the
// default indent unit, unless a wider/narrower expansion was what matched.
// Empty string means not found.
func findActualString(fileContent, searchString string) (string, int) {
	if actual, w := findActualStringCore(fileContent, searchString); actual != "" {
		return actual, w
	}
	if looksLikeLineNumberPrefixed(searchString) {
		return findActualStringCore(fileContent, stripLineNumberPrefixes(searchString))
	}
	return "", 0
}

// findActualStringCore is the exact/quote/indent/trailing-whitespace fallback
// chain. See findActualString for the line-number-prefix retry wrapped
// around it.
func findActualStringCore(fileContent, searchString string) (string, int) {
	// First try exact match
	if strings.Contains(fileContent, searchString) {
		return searchString, defaultIndentWidth
	}

	// Try with normalized quotes
	normalizedSearch := normalizeQuotes(searchString)
	normalizedFile := normalizeQuotes(fileContent)

	idx := strings.Index(normalizedFile, normalizedSearch)
	if idx != -1 {
		// Map the index back to the original file content.
		// Since normalization only replaces runes with ASCII equivalents
		// of the same byte length (UTF-8 curly quotes are 3 bytes each,
		// straight quotes are 1 byte), we can't use byte offsets directly.
		// Instead, scan through the original file rune-by-rune.
		fileRunes := []rune(fileContent)
		normalizedRunes := []rune(normalizedFile)
		searchRunes := []rune(normalizedSearch)

		// Verify the match at the rune level in normalized space
		if idx+len(searchRunes) > len(normalizedRunes) {
			return "", 0
		}
		for i := 0; i < len(searchRunes); i++ {
			if normalizedRunes[idx+i] != searchRunes[i] {
				return "", 0
			}
		}

		// Map rune index back to original file substring
		// Count runes in original file up to the match position
		origStart := -1
		runeCount := 0
		for i := range fileRunes {
			if runeCount == idx {
				origStart = i
				break
			}
			runeCount++
		}
		if origStart == -1 {
			return "", 0
		}

		origEnd := -1
		runeCount = 0
		for i := range fileRunes {
			if runeCount == idx+len(searchRunes) {
				origEnd = i
				break
			}
			runeCount++
		}
		if origEnd == -1 {
			origEnd = len(fileRunes)
		}

		return string(fileRunes[origStart:origEnd]), defaultIndentWidth
	}

	// Try with normalized leading whitespace (tab → space expansion).
	// Several widths are tried because the width the model assumed when it
	// turned tabs into spaces is unknowable.
	for _, w := range indentWidths {
		if actual := findWithIndentNorm(fileContent, searchString, w); actual != "" {
			return actual, w
		}
	}

	// Try ignoring trailing whitespace per line — a model that visually
	// copied a line rarely reproduces trailing spaces it can't see.
	if actual := findWithTrailingWSNorm(fileContent, searchString); actual != "" {
		return actual, defaultIndentWidth
	}

	return "", 0
}

// defaultIndentWidth is the tab width assumed when no whitespace
// normalization was needed to find the match — it matches how read_file and
// the terminal tool render tabs.
const defaultIndentWidth = 4

// normalizeLeadingWhitespace expands tabs in leading whitespace of each line
// to width-space stops, making tab-indented and space-indented text comparable.
// Only leading whitespace is touched — tabs inside line content are left alone.
func normalizeLeadingWhitespace(s string, width int) string {
	var b strings.Builder
	b.Grow(len(s) + len(s)/10)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed != line {
			leadingLen := len(line) - len(trimmed)
			leading := line[:leadingLen]
			b.WriteString(strings.ReplaceAll(leading, "\t", strings.Repeat(" ", width)))
		}
		b.WriteString(trimmed)
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// normalizeTrailingWhitespace strips trailing spaces/tabs/CR from every line.
// Used to make a match tolerant of trailing whitespace on the file's side
// that a model copying text by eye wouldn't have reproduced in old_string.
func normalizeTrailingWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r")
	}
	return strings.Join(lines, "\n")
}

// findWithLineNorm matches searchString against fileContent after applying
// normalize to both, and returns the corresponding substring from the
// original (unnormalized) fileContent so the edit preserves the file's
// actual formatting. The matching is line-based because both whitespace
// normalizations in use here (leading-indent and trailing-whitespace) only
// affect per-line content, and results need to map back to whole lines in
// the original file.
func findWithLineNorm(fileContent, searchString string, normalize func(string) string) string {
	// The matching is line-based, so a trailing newline on the search string
	// would otherwise become an empty last line that must equal the file's
	// next line. Strip it for matching and restore it from the file after.
	search := searchString
	wantTrailingNL := strings.HasSuffix(search, "\n")
	if wantTrailingNL {
		search = strings.TrimSuffix(search, "\n")
	}

	normFile := normalize(fileContent)
	normSearch := normalize(search)

	if !strings.Contains(normFile, normSearch) {
		return ""
	}

	fileLines := strings.Split(fileContent, "\n")
	normFileLines := strings.Split(normFile, "\n")
	normSearchLines := strings.Split(normSearch, "\n")

	start := -1
	for i := 0; i <= len(normFileLines)-len(normSearchLines); i++ {
		match := true
		for j := 0; j < len(normSearchLines); j++ {
			if normFileLines[i+j] != normSearchLines[j] {
				match = false
				break
			}
		}
		if match {
			start = i
			break
		}
	}

	if start == -1 {
		return ""
	}

	end := start + len(normSearchLines)
	actual := strings.Join(fileLines[start:end], "\n")

	if wantTrailingNL {
		// The search demands a newline after the last matched line; only
		// honor it if the file actually has one there (i.e. the match did
		// not land on an unterminated final line).
		if end >= len(fileLines) {
			return ""
		}
		actual += "\n"
	}

	return actual
}

// findWithIndentNorm matches searchString against fileContent after
// normalizing leading whitespace in both at the given tab width, so that
// space-indented old_string from the LLM still matches tab-indented files.
func findWithIndentNorm(fileContent, searchString string, width int) string {
	return findWithLineNorm(fileContent, searchString, func(s string) string {
		return normalizeLeadingWhitespace(s, width)
	})
}

// findWithTrailingWSNorm matches searchString against fileContent ignoring
// trailing whitespace differences per line.
func findWithTrailingWSNorm(fileContent, searchString string) string {
	return findWithLineNorm(fileContent, searchString, normalizeTrailingWhitespace)
}

// isAllDigits reports whether s is non-empty and every rune is 0-9.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// looksLikeLineNumberPrefixed reports whether every non-empty line of s
// starts with a "digits + tab" prefix — the cat-n-style column read_file
// prepends to every line for display (see ReadFileTool). A model that
// pastes read_file's output straight into old_string instead of the file's
// raw bytes produces exactly this shape; a real source or data file is very
// unlikely to have every single line begin with bare digits then a tab.
func looksLikeLineNumberPrefixed(s string) bool {
	lines := strings.Split(s, "\n")
	seenAny := false
	for _, line := range lines {
		if line == "" {
			continue
		}
		tabIdx := strings.IndexByte(line, '\t')
		if tabIdx <= 0 {
			return false
		}
		if !isAllDigits(strings.TrimSpace(line[:tabIdx])) {
			return false
		}
		seenAny = true
	}
	return seenAny
}

// stripLineNumberPrefixes removes a leading "digits + tab" column from every
// line of s. Only call this once looksLikeLineNumberPrefixed has confirmed
// the shape — it unconditionally cuts at the first tab on lines that have one.
func stripLineNumberPrefixes(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if tabIdx := strings.IndexByte(line, '\t'); tabIdx > 0 {
			lines[i] = line[tabIdx+1:]
		}
	}
	return strings.Join(lines, "\n")
}

// normalizeCRLF collapses "\r\n" to "\n" so old_string — which is always LF,
// since read_file's bufio.Scanner strips "\r" before the model ever sees a
// line — can match a CRLF file. starts records, for every byte of norm, the
// offset in body it came from; starts[len(norm)] == len(body). Any norm
// range [a,b) therefore maps back to the original byte range
// [starts[a], starts[b]), which Execute uses to splice the replacement
// directly into the untouched original bytes instead of rewriting the whole
// file through a global LF<->CRLF round trip — the latter used to flip the
// line ending of every "\n" in the file just because *some* other line used
// "\r\n" (see the edit_file audit's Bug 1: a mixed-line-ending file had
// unrelated lines silently rewritten by every edit).
func normalizeCRLF(body string) (norm string, starts []int) {
	var b strings.Builder
	b.Grow(len(body))
	starts = make([]int, 0, len(body)+1)
	i := 0
	for i < len(body) {
		starts = append(starts, i)
		if body[i] == '\r' && i+1 < len(body) && body[i+1] == '\n' {
			b.WriteByte('\n')
			i += 2
		} else {
			b.WriteByte(body[i])
			i++
		}
	}
	starts = append(starts, len(body))
	return b.String(), starts
}

// lineEndingAt reports the line-ending convention ("\n" or "\r\n") to apply
// to any newline inside a replacement spliced into body at [origStart,
// origEnd). It prefers the convention of the span being replaced, falling
// back to the nearest neighboring line break, so only the edited region's
// own convention is ever touched — never the rest of a mixed-line-ending
// file.
func lineEndingAt(body string, origStart, origEnd int) string {
	if strings.Contains(body[origStart:origEnd], "\r\n") {
		return "\r\n"
	}
	if idx := strings.IndexByte(body[origEnd:], '\n'); idx != -1 {
		abs := origEnd + idx
		if abs > 0 && body[abs-1] == '\r' {
			return "\r\n"
		}
		return "\n"
	}
	if idx := strings.LastIndexByte(body[:origStart], '\n'); idx != -1 {
		if idx > 0 && body[idx-1] == '\r' {
			return "\r\n"
		}
		return "\n"
	}
	return "\n"
}

func (EditFileTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	path, _ := input["path"].(string)
	if strings.TrimSpace(path) == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("edit_file: path is required")
	}
	oldStr, ok1 := input["old_string"].(string)
	newStr, ok2 := input["new_string"].(string)
	if !ok1 {
		return agent.ToolResult{Text: ""}, fmt.Errorf("edit_file: old_string is required")
	}
	if !ok2 {
		return agent.ToolResult{Text: ""}, fmt.Errorf("edit_file: new_string is required (use empty string to delete)")
	}
	if oldStr == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("edit_file: old_string must be non-empty")
	}
	if oldStr == newStr {
		return agent.ToolResult{Text: ""}, fmt.Errorf("edit_file: old_string and new_string are identical — nothing to do")
	}
	replaceAll, _ := input["replace_all"].(bool)

	abs, err := resolvePathIn(WorkingDir(ctx), path)
	if err != nil {
		return agent.ToolResult{Text: ""}, err
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("edit_file: read %q: %w", path, err)
	}
	body := string(data)

	// Match in \n-normalized space (see normalizeCRLF) while keeping starts[]
	// to map any match back to the original bytes, so a mixed-line-ending
	// file can be edited without flipping the ending of lines the edit never
	// touched.
	normBody, starts := normalizeCRLF(body)

	// Strip trailing whitespace from new_string (except for markdown files
	// where trailing double-space is a hard line break).
	isMarkdown := strings.HasSuffix(strings.ToLower(abs), ".md") ||
		strings.HasSuffix(strings.ToLower(abs), ".mdx")
	newStrClean := newStr
	if !isMarkdown {
		newStrClean = stripTrailingWhitespace(newStr)
	}

	// Refuse to inject a live-credential shape — same guard write_file applies,
	// so a secret can't slip in through the edit path instead. Only the new text
	// is scanned (not the whole file): a pre-existing match the model didn't
	// introduce shouldn't block an unrelated edit elsewhere in the file.
	if secret := scanForSecrets(newStrClean); secret != "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("edit_file: refusing to write new_string that contains a %s. "+
			"If this is genuinely intended (e.g. a test fixture), remove the live-credential "+
			"shape or create the file outside the agent.", secret)
	}

	// Use findActualString for quote-, indent-, and trailing-whitespace
	// normalized matching, plus a line-number-prefix-stripped retry.
	actualOldStr, indentWidth := findActualString(normBody, oldStr)
	if actualOldStr == "" {
		// Build a helpful error message showing what we tried
		msg := fmt.Sprintf("edit_file: old_string not found in %s", path)

		// If quote normalization was attempted but still failed, hint at it
		if normalizeQuotes(oldStr) != oldStr {
			msg += " (also tried with normalized quotes)"
		}
		if looksLikeLineNumberPrefixed(oldStr) {
			msg += " (old_string looks like it includes read_file's line-number prefix — " +
				"old_string must be the raw file content, without the \"NNNNNN\\t\" column " +
				"read_file adds for display)"
		}
		return agent.ToolResult{Text: ""}, fmt.Errorf("%s", msg)
	}

	// Locate every occurrence's [start,end) in normBody up front: replace_all
	// needs them all, and the "matches N times" error needs their line
	// numbers so the caller can find the ambiguity without guessing.
	var normRanges [][2]int
	searchFrom := 0
	for {
		idx := strings.Index(normBody[searchFrom:], actualOldStr)
		if idx == -1 {
			break
		}
		start := searchFrom + idx
		end := start + len(actualOldStr)
		normRanges = append(normRanges, [2]int{start, end})
		searchFrom = end
	}
	count := len(normRanges)
	if count == 0 {
		// Should not happen after findActualString succeeded, but be safe
		return agent.ToolResult{Text: ""}, fmt.Errorf("edit_file: old_string not found in %s", path)
	}
	if count > 1 && !replaceAll {
		lineNos := make([]string, count)
		for i, r := range normRanges {
			lineNos[i] = fmt.Sprintf("%d", strings.Count(normBody[:r[0]], "\n")+1)
		}
		return agent.ToolResult{Text: ""}, fmt.Errorf(
			"edit_file: old_string matches %d times (lines %s) — either include more context to make it unique, or set replace_all=true",
			count, strings.Join(lineNos, ", "),
		)
	}

	// Preserve curly quote style from the matched text when the LLM
	// provided straight quotes but the file uses curly quotes.
	actualNewStr := preserveQuoteStyle(oldStr, actualOldStr, newStrClean)
	// Preserve file indent convention (tabs vs spaces) — when the file
	// uses tabs but the LLM supplied space-indented new_string.
	actualNewStr = preserveIndentStyle(actualOldStr, actualNewStr, indentWidth)

	// Splice each occurrence directly into the original bytes, applying the
	// line-ending convention local to that occurrence's own span — see
	// lineEndingAt. Everything between occurrences (and before/after all of
	// them) is copied through byte-for-byte from the original file.
	var out strings.Builder
	prevOrigEnd := 0
	for _, r := range normRanges {
		origStart, origEnd := starts[r[0]], starts[r[1]]
		out.WriteString(body[prevOrigEnd:origStart])
		repl := actualNewStr
		if lineEndingAt(body, origStart, origEnd) == "\r\n" {
			repl = strings.ReplaceAll(repl, "\n", "\r\n")
		}
		out.WriteString(repl)
		prevOrigEnd = origEnd
	}
	out.WriteString(body[prevOrigEnd:])
	updated := out.String()

	// Stage the pre-edit version into the trash so an edit that destroys
	// uncommitted work is recoverable (skipped for git-tracked-clean files).
	backupBeforeOverwrite(ctx, abs)
	if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("edit_file: write %q: %w", path, err)
	}

	occurrences := 1
	if replaceAll {
		occurrences = count
	}
	ui := map[string]any{
		"type":        "edit",
		"path":        abs,
		"occurrences": occurrences,
		"diff":        EditUIDiff(actualOldStr, actualNewStr),
	}
	if replaceAll {
		return agent.ToolResult{Text: fmt.Sprintf("Replaced %d occurrence(s) in %s", count, abs), UI: ui}, nil
	}
	return agent.ToolResult{Text: fmt.Sprintf("Replaced 1 occurrence in %s", abs), UI: ui}, nil
}

// EditUIDiff renders the replacement as a removed/added block for the web
// UI's diff view. The edit is an exact substring swap, so old/new lines ARE
// the change — no diff algorithm needed. Exported so the server package can
// reuse it to preview the pending edit in the permission-ask confirmation
// (see Server.permissionAskFrom), not just the post-execution result card.
func EditUIDiff(oldStr, newStr string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimRight(oldStr, "\n"), "\n") {
		b.WriteString("- " + l + "\n")
	}
	if newStr != "" {
		for _, l := range strings.Split(strings.TrimRight(newStr, "\n"), "\n") {
			b.WriteString("+ " + l + "\n")
		}
	}
	return uiHead(b.String(), 24, 1600)
}

// preserveQuoteStyle copies curly quote style from actualOldStr to newStr
// when the LLM provided straight quotes but the file uses curly quotes.
func preserveQuoteStyle(oldStr, actualOldStr, newStr string) string {
	// If no normalization happened, return as-is
	if oldStr == actualOldStr {
		return newStr
	}

	// Detect which curly quote types were in the file
	hasDoubleQuotes := strings.ContainsRune(actualOldStr, leftDoubleCurlyQuote) ||
		strings.ContainsRune(actualOldStr, rightDoubleCurlyQuote)
	hasSingleQuotes := strings.ContainsRune(actualOldStr, leftSingleCurlyQuote) ||
		strings.ContainsRune(actualOldStr, rightSingleCurlyQuote)

	if !hasDoubleQuotes && !hasSingleQuotes {
		return newStr
	}

	result := newStr
	if hasDoubleQuotes {
		result = applyCurlyDoubleQuotes(result)
	}
	if hasSingleQuotes {
		result = applyCurlySingleQuotes(result)
	}
	return result
}

func isOpeningContext(chars []rune, index int) bool {
	if index == 0 {
		return true
	}
	prev := chars[index-1]
	return prev == ' ' || prev == '\t' || prev == '\n' || prev == '\r' ||
		prev == '(' || prev == '[' || prev == '{' ||
		prev == '—' || prev == '–'
}

func applyCurlyDoubleQuotes(str string) string {
	chars := []rune(str)
	result := make([]rune, len(chars))
	for i, ch := range chars {
		if ch == '"' {
			if isOpeningContext(chars, i) {
				result[i] = leftDoubleCurlyQuote
			} else {
				result[i] = rightDoubleCurlyQuote
			}
		} else {
			result[i] = ch
		}
	}
	return string(result)
}

func applyCurlySingleQuotes(str string) string {
	chars := []rune(str)
	result := make([]rune, len(chars))
	for i, ch := range chars {
		if ch == '\'' {
			// Don't convert apostrophes in contractions (e.g., "don't", "it's")
			prevIsLetter := i > 0 && isLetter(chars[i-1])
			nextIsLetter := i < len(chars)-1 && isLetter(chars[i+1])
			if prevIsLetter && nextIsLetter {
				result[i] = rightSingleCurlyQuote
			} else if isOpeningContext(chars, i) {
				result[i] = leftSingleCurlyQuote
			} else {
				result[i] = rightSingleCurlyQuote
			}
		} else {
			result[i] = ch
		}
	}
	return string(result)
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// preserveIndentStyle converts leading whitespace in newStr to match the
// file's indentation convention (detected from actualOldStr). When the file
// uses tabs and the LLM supplied space-indented new_string, each run of
// `width` spaces becomes one tab — width being the tab width that located the
// match, so the conversion mirrors the expansion. Remainder spaces (alignment,
// or a partial level) are kept rather than truncated away, so a 2-space line
// is never silently stripped to column zero. Lines whose leading whitespace
// already contains a tab are left untouched: the model followed the file's
// convention, and rewriting them would flatten tab+space alignment.
func preserveIndentStyle(actualOldStr, newStr string, width int) string {
	// Only convert when the file uses tabs for indentation
	if !strings.Contains(actualOldStr, "\n\t") && !strings.HasPrefix(actualOldStr, "\t") {
		return newStr
	}

	newLines := strings.Split(newStr, "\n")
	for i, line := range newLines {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == line {
			continue
		}
		leading := line[:len(line)-len(trimmed)]
		if strings.Contains(leading, "\t") {
			continue
		}
		tabs := len(leading) / width
		rem := len(leading) % width
		newLines[i] = strings.Repeat("\t", tabs) + strings.Repeat(" ", rem) + trimmed
	}
	return strings.Join(newLines, "\n")
}
