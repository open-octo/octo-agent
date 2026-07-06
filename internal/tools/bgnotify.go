package tools

import (
	"fmt"
	"strings"
	"time"
)

// shortAsyncDuration is the threshold below which an "async" background
// launch is flagged as unnecessary. The synchronous terminal path already
// auto-promotes to background on its own if a command runs past
// TerminalTimeout, so there's never a need to predict "will this be slow?"
// up front — a command that turns out to finish in a couple of seconds
// would have returned its output in the very same tool call had it just
// run synchronously, with no extra id, no detached-process bookkeeping, and
// no second turn spent waiting for this very notification. Ten seconds is
// comfortably below TerminalTimeout (120s) and well above normal tool-call
// jitter, so it only fires for launches that clearly didn't need
// backgrounding, not ones that were a reasonable judgment call.
const shortAsyncDuration = 10 * time.Second

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
	if e.Mode == BgModeAsync && e.Duration > 0 && e.Duration < shortAsyncDuration {
		fmt.Fprintf(&b,
			"\n\n[Note: this finished in %s — that's fast enough it didn't need run_in_background at all. "+
				"A synchronous terminal call (no run_in_background) would have returned this same output "+
				"immediately, in the same turn, and it auto-promotes to background on its own if a command "+
				"turns out to run long — so there's no need to guess duration up front. Reserve "+
				"run_in_background:\"async\" for commands you have concrete reason to expect will run well "+
				"past a few seconds, or when you have other independent work to do while it runs.]",
			e.Duration.Round(100*time.Millisecond))
	}
	b.WriteString("\n</system-reminder>")
	return b.String()
}

// FormatBgNoteWithSummary renders a completion notice plus a summary of other
// background processes still running. The summary helps the model track
// in-flight async/interactive work without a dedicated process-list tool. Pass
// nil for mgr to omit the summary (e.g. unit tests that only verify the basic
// note format).
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
