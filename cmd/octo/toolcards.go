package main

import (
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/tools"
	"github.com/open-octo/octo-agent/internal/tui"
)

// outputCardMaxLines caps how many output lines a tool-result card shows
// before collapsing the rest into an "… +N lines" marker. Kept small (Claude
// Code shows ~3) — the full output went to the model, the card is a glimpse.
const outputCardMaxLines = 4

// cardVerbFor maps a tool name to the verb shown in its result card, or ""
// for tools that render as a terse one-line status instead of a card. This is
// the set of tools the TUI renders as rich cards (the plain/headless path
// always uses one-liners — see dev-docs/tui-ux-upgrade-design.md decision #8).
func cardVerbFor(toolName string) string {
	switch toolName {
	case "edit_file":
		return "Update"
	case "terminal":
		return "Run"
	case "terminal_output":
		return "Check"
	case "kill_shell":
		return "Kill"
	case "terminal_input":
		return "Input"
	case "grep":
		return "Grep"
	case "web_search":
		return "Search"
	case "glob":
		return "Glob"
	case "read_file":
		return "Read"
	case "write_file":
		return "Write"
	case "web_fetch":
		return "Fetch"
	}
	return ""
}

// cardTargetFor extracts the card header's target (command / pattern / path /
// url) from a tool's input, falling back to a one-line input summary.
func cardTargetFor(toolName string, input map[string]any) string {
	var key string
	switch toolName {
	case "terminal":
		key = "command"
	case "terminal_output", "kill_shell", "terminal_input":
		if id, ok := input["id"].(string); ok && id != "" {
			// Show the command as the name; fall back to the internal id only
			// when the process is already gone and its command can't be resolved.
			if cmd, found := tools.BgCommand(id); found && cmd != "" {
				return truncate1Line(cmd)
			}
			return id
		}
	case "grep", "glob":
		key = "pattern"
	case "web_search":
		key = "query"
	case "read_file", "edit_file", "write_file":
		key = "path"
	case "web_fetch":
		key = "url"
	}
	if key != "" {
		if s, ok := input[key].(string); ok && s != "" {
			return truncate1Line(s)
		}
	}
	return summariseInput(input)
}

// renderToolCard returns the rich card for a finished tool call, or "" if the
// tool isn't a card tool (caller falls back to a one-line status). edit_file
// success renders a diff card; everything else (and edit_file errors) renders
// an output-preview card. width clips card rows to the terminal; elapsed (>0)
// is shown dimmed in the header. TUI-only — the plain path never calls this.
func renderToolCard(toolName string, input map[string]any, output string, isErr bool, width int, elapsed time.Duration) string {
	verb := cardVerbFor(toolName)
	if verb == "" {
		return ""
	}
	if card, ok := renderEditFileCard(toolName, input, isErr, width); ok {
		return card
	}
	if toolName == "read_file" && !isErr {
		// A content preview here is usually the file's package/import
		// boilerplate — near-zero information for the screen space it costs.
		// A one-liner with the line count (and a click-to-open link to the
		// full content, matching the #1093 fold-link pattern) is cheaper and
		// no less useful (#1097).
		n := readFileLineCount(output)
		link := ""
		// Only spill to disk when there's actually folded content behind the
		// link — same threshold the generic card path uses to decide whether
		// to fold at all. Spilling every read unconditionally, even a 1-line
		// read, would widen every file the agent touches into a plaintext
		// copy under ~/.octo/tmp for no reason: nothing is hidden when the
		// whole thing already fits in the one-liner.
		if n > outputCardMaxLines {
			if path, err := tools.WriteCardSpill(toolName, output); err == nil {
				link = tui.FileURI(path)
			}
		}
		return tui.RenderReadFileStatus(cardTargetFor(toolName, input), n, formatElapsed(elapsed), link, width)
	}
	output, opts := toolCardOpts(toolName, input, output, isErr, width, elapsed)
	// When the card will fold ("… +N lines"), persist the full output and
	// hyperlink the marker to it — otherwise the folded content is
	// unrecoverable in the TUI (issue #1093). Write failure degrades to the
	// plain marker.
	if opts.MaxLines > 0 && nonBlankLineCount(output) > opts.MaxLines {
		if path, err := tools.WriteCardSpill(toolName, output); err == nil {
			opts.FoldLink = tui.FileURI(path)
		}
	}
	return tui.RenderOutputCard(verb, cardTargetFor(toolName, input), output, opts)
}

// toolCardOpts builds the per-tool OutputCardOpts (tail direction, exit-code
// lifting, syntax language, meta) shared by renderToolCard's capped card and
// renderToolFull's uncapped re-print — everything except the fold cap itself,
// which the two callers apply differently. Returns output as well since the
// terminal case rewrites it (splitTerminalExit strips the trailing marker).
func toolCardOpts(toolName string, input map[string]any, output string, isErr bool, width int, elapsed time.Duration) (string, tui.OutputCardOpts) {
	opts := tui.OutputCardOpts{
		MaxLines: outputCardMaxLines,
		Width:    width,
		IsErr:    isErr,
		Meta:     formatElapsed(elapsed),
	}
	switch toolName {
	case "read_file":
		if p, _ := input["path"].(string); p != "" {
			opts.Language = tui.GuessLanguage(p)
		}
	case "write_file":
		// write_file's own output is already a human-readable summary
		// ("Wrote N bytes (M lines) to /path"); show that instead of a
		// redundant content preview.
		opts.MaxLines = 0
	case "terminal":
		// Command output: errors and summaries land at the bottom, so show the
		// tail. A non-zero exit is reported as a trailing "[exit: …]" marker,
		// not a tool error — lift it into the header (red bullet + meta) so a
		// failed command never renders as a green card with the marker folded
		// away.
		opts.Tail = true
		if body, exit := splitTerminalExit(output); exit != "" {
			output = body
			opts.IsErr = true
			opts.Meta = joinMeta(formatExitReason(exit), opts.Meta)
		}
	case "terminal_output", "kill_shell", "terminal_input":
		opts.Tail = true
	}
	return output, opts
}

// renderToolFull re-renders a finished tool call with its output fully
// uncapped, for the /transcript command's fallback recovery path — the fold
// marker's hyperlink (see renderToolCard) already recovers folded output, but
// only on terminals with OSC 8 support; this reprints the same call natively
// in-terminal instead (issue #1093).
func renderToolFull(toolName string, input map[string]any, output string, isErr bool, width int) string {
	verb := cardVerbFor(toolName)
	if verb == "" {
		return ""
	}
	if card, ok := renderEditFileCard(toolName, input, isErr, width); ok {
		return card
	}
	output, opts := toolCardOpts(toolName, input, output, isErr, width, 0)
	opts.MaxLines = 0
	return tui.RenderOutputCard(verb, cardTargetFor(toolName, input), output, opts)
}

// renderEditFileCard returns edit_file's diff card when applicable (success
// only — an edit_file error falls through to the ordinary output-preview
// path like every other tool), or ok=false otherwise. Shared by
// renderToolCard and renderToolFull so the two can never diverge on this
// special case.
func renderEditFileCard(toolName string, input map[string]any, isErr bool, width int) (string, bool) {
	if toolName != "edit_file" || isErr {
		return "", false
	}
	path, _ := input["path"].(string)
	oldS, _ := input["old_string"].(string)
	newS, _ := input["new_string"].(string)
	return tui.RenderEditCard(path, oldS, newS, width), true
}

// nonBlankLineCount mirrors RenderOutputCard's blank-line filtering so the
// caller can predict whether the card will fold before rendering it.
func nonBlankLineCount(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// readFileLineCount counts the numbered content lines in a read_file result
// ("%6d\t<line>", per internal/tools/read_file.go), excluding the bracketed
// footer markers ("[end of file: …]", "[truncated: …]", "[empty file]") the
// tool appends — those aren't file content and would otherwise inflate the
// count by one.
func readFileLineCount(output string) int {
	n := 0
	for _, l := range strings.Split(output, "\n") {
		if l == "" || strings.HasPrefix(strings.TrimLeft(l, " "), "[") {
			continue
		}
		n++
	}
	return n
}

// splitTerminalExit detaches the trailing "[exit: …]" marker the terminal
// tool appends on a non-zero exit (internal/tools/terminal.go). It returns
// the output without the marker line plus the raw exit text embedded in the
// marker — Go's *exec.ExitError.Error() string passed straight through,
// e.g. "exit status 1" for a normal nonzero exit or "signal: killed" for a
// killed process, never a bare code — or (output, "") when no marker is
// present.
func splitTerminalExit(output string) (body, exit string) {
	trimmed := strings.TrimRight(output, "\n")
	last := trimmed
	if i := strings.LastIndexByte(trimmed, '\n'); i >= 0 {
		last = trimmed[i+1:]
	}
	if !strings.HasPrefix(last, "[exit: ") || !strings.HasSuffix(last, "]") {
		return output, ""
	}
	exit = strings.TrimSuffix(strings.TrimPrefix(last, "[exit: "), "]")
	body = strings.TrimRight(trimmed[:len(trimmed)-len(last)], "\n")
	return body, exit
}

// formatExitReason turns splitTerminalExit's raw exit text into the card
// header meta string. The text is Go's *exec.ExitError.Error() passed
// through verbatim, so "exit status N" — not a bare "N" — is what a normal
// nonzero exit actually looks like; without stripping that wrapper, the
// header rendered the redundant "exit exit status 1" for every ordinary
// failing command (#1146, found while fixing the identical bug in the web
// client's equivalent card, #1106/#1145). "signal: NAME" already reads fine
// standalone and is returned as-is, not double-prefixed with "exit ".
func formatExitReason(exit string) string {
	if code, ok := strings.CutPrefix(exit, "exit status "); ok {
		return "exit " + code
	}
	return exit
}

// formatElapsed renders a tool call's duration for the card header: "" when
// unknown (<=0, e.g. history replay) or too quick to be worth annotating
// (sub-100ms would render as a meaningless "0s"), sub-second precision while
// short, whole seconds once rounding noise stops mattering.
func formatElapsed(d time.Duration) string {
	switch {
	case d < 100*time.Millisecond:
		return ""
	case d < 10*time.Second:
		return d.Round(100 * time.Millisecond).String()
	default:
		return d.Round(time.Second).String()
	}
}

// joinMeta joins non-empty header annotations with " · ".
func joinMeta(parts ...string) string {
	kept := parts[:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " · ")
}
