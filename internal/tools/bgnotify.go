package tools

import (
	"fmt"
	"strings"
)

// FormatBgNote renders a background-process completion as a <system-reminder>
// block. It rides the steer path of whichever frontend wires it (CLI/TUI:
// Inbox.Enqueue; server: Inbox or steer queue; IM: the session agent's
// Inbox), so the model reads it as an environment event rather than user
// speech. Wrapping in <system-reminder> matches octo's convention for
// injected, non-user context — UIs strip these spans from user-visible text.
func FormatBgNote(e BgExit) string {
	var b strings.Builder
	b.WriteString("<system-reminder>\n")
	b.WriteString("[BACKGROUND COMPLETED]\n")
	fmt.Fprintf(&b, "Background process %s (`%s`) %s.", e.ID, e.Command, e.Status)
	if out := strings.TrimRight(e.NewOutput, "\n"); out != "" {
		b.WriteString("\nOutput since last check:\n")
		// A long-running build/test that finished in the background can emit
		// far more than fits a single notice — spill it to a temp file and
		// show a head+tail preview, same as the synchronous terminal path.
		b.WriteString(MaybeSpillOutput(e.ID, out))
	} else {
		b.WriteString("\n(no new output)")
	}
	b.WriteString("\n</system-reminder>")
	return b.String()
}

// FormatBgNoteWithSummary renders a completion notice plus a summary of other
// background processes still running. The summary helps the model track
// in-flight async/interactive work without needing a separate terminal_list
// poll. Pass nil for mgr to omit the summary (legacy tests / unknown scope).
func FormatBgNoteWithSummary(mgr *BackgroundManager, e BgExit) string {
	note := FormatBgNote(e)
	if mgr == nil {
		return note
	}
	summary := runningBackgroundSummary(mgr, e.ID)
	if summary == "" {
		return note
	}
	// Inject the summary before the closing </system-reminder> tag.
	return strings.Replace(note, "\n</system-reminder>", "\n"+summary+"\n</system-reminder>", 1)
}

// runningBackgroundSummary returns a human-readable line listing other running
// async and interactive background tasks, excluding the one that just finished.
// Empty string if there are no other running tasks.
func runningBackgroundSummary(mgr *BackgroundManager, excludeID string) string {
	var async []BgInfo
	var interactive []BgInfo
	for _, in := range mgr.ListRunning() {
		if in.ID == excludeID {
			continue
		}
		if in.Mode == BgModeAsync {
			async = append(async, in)
		} else {
			interactive = append(interactive, in)
		}
	}
	if len(async) == 0 && len(interactive) == 0 {
		return ""
	}

	var parts []string
	if len(async) > 0 {
		parts = append(parts, formatBgGroup(len(async), "async", async))
	}
	if len(interactive) > 0 {
		parts = append(parts, formatBgGroup(len(interactive), "interactive", interactive))
	}
	return "Still running: " + strings.Join(parts, ", ") + "."
}

func formatBgGroup(n int, label string, infos []BgInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s", n, label)
	if n != 1 {
		b.WriteString("s")
	}
	b.WriteString(" (")
	for i, in := range infos {
		if i > 0 {
			b.WriteString("; ")
		}
		cmd := in.Command
		if len(cmd) > 40 {
			cmd = cmd[:37] + "..."
		}
		fmt.Fprintf(&b, "%s `%s`", in.ID, cmd)
	}
	b.WriteString(")")
	return b.String()
}
