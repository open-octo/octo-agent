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
	if toolName == "edit_file" && !isErr {
		path, _ := input["path"].(string)
		oldS, _ := input["old_string"].(string)
		newS, _ := input["new_string"].(string)
		return tui.RenderEditCard(path, oldS, newS, width)
	}
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
			opts.Meta = joinMeta("exit "+exit, opts.Meta)
		}
	case "terminal_output", "kill_shell", "terminal_input":
		opts.Tail = true
	}
	return tui.RenderOutputCard(verb, cardTargetFor(toolName, input), output, opts)
}

// splitTerminalExit detaches the trailing "[exit: …]" marker the terminal
// tool appends on a non-zero exit (internal/tools/terminal.go). It returns
// the output without the marker line plus the exit text ("1", "signal:
// killed"), or (output, "") when no marker is present.
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

// formatElapsed renders a tool call's duration for the card header: "" when
// unknown (<=0, e.g. history replay), sub-second precision while short, whole
// seconds once rounding noise stops mattering.
func formatElapsed(d time.Duration) string {
	switch {
	case d <= 0:
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
