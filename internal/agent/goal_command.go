package agent

import (
	"fmt"
	"strings"
)

// GoalCommandStart says whether a command left the goal ready to be pursued
// right now, and how the turn that follows should be described.
type GoalCommandStart int

const (
	// GoalStartNone: nothing to start — the command read, paused, cleared or
	// edited the goal.
	GoalStartNone GoalCommandStart = iota
	// GoalStartFresh: a brand-new goal that has never run a turn (create,
	// replace). The TUI announces this one as "Goal starts".
	GoalStartFresh
	// GoalStartResumed: an existing goal picking back up after a pause. The
	// TUI announces it as "Goal continues", the same word a turn-end
	// continuation uses.
	GoalStartResumed
)

// GoalCommandUsage is the one-line grammar hint for the text-reply form of
// the command (the web slash command's notice).
const GoalCommandUsage = "Usage: /goal <objective> · /goal edit <objective> · /goal pause|resume|clear · /goal replace <objective>"

// GoalCommand applies a "/goal …" command to the session and returns a plain
// text reply — the web composer's surface, which renders it as a scrollback
// notice.
// The grammar matches the TUI except `edit`, which takes the new objective
// inline: the web has no input-prefill to hand the current objective back for
// editing.
//
// start reports whether the command left the goal ready to be pursued right
// now — created, replaced, or resumed. The TUI starts the continuation turn
// itself for exactly these three (startGoalNow); transports that can kick an
// idle turn gate on it to match. It says nothing about whether a turn should
// actually run: that stays GoalContinuation's call.
//
// The TUI keeps its own richer dispatcher (prefilled edit, styled summary);
// the semantics here and there must stay aligned.
func GoalCommand(s *Session, args string) (reply string, start GoalCommandStart) {
	args = strings.TrimSpace(args)
	sub := strings.ToLower(args)

	switch {
	case args == "":
		g, ok := s.GoalSnapshot()
		if !ok {
			return "No goal is currently set. " + GoalCommandUsage, GoalStartNone
		}
		return goalCommandSummary(g), GoalStartNone

	case sub == "pause":
		g, err := s.SetGoalStatus(GoalPaused)
		if err != nil {
			return "/goal pause: " + err.Error(), GoalStartNone
		}
		return "Goal " + GoalStatusLabel(g.Status) + " — " + goalObjectiveLine(g.Objective), GoalStartNone

	case sub == "resume":
		g, err := s.SetGoalStatus(GoalActive)
		if err != nil {
			return "/goal resume: " + err.Error(), GoalStartNone
		}
		return "Goal " + GoalStatusLabel(g.Status) + " — " + goalObjectiveLine(g.Objective), GoalStartResumed

	case sub == "clear":
		if s.ClearGoal() {
			return "Goal cleared", GoalStartNone
		}
		return "No goal to clear", GoalStartNone

	case sub == "edit":
		return "Usage: /goal edit <objective> — rewrites the objective, keeping usage and budget", GoalStartNone

	case strings.HasPrefix(sub, "edit "):
		// No kick: editing hands the running or next turn a one-time steer
		// rather than starting fresh work, matching the TUI's edit path.
		g, err := s.EditGoalObjective(strings.TrimSpace(args[len("edit "):]))
		if err != nil {
			return "/goal edit: " + err.Error(), GoalStartNone
		}
		return "Goal updated — " + goalObjectiveLine(g.Objective), GoalStartNone

	case sub == "replace":
		return "Usage: /goal replace <objective>", GoalStartNone

	case strings.HasPrefix(sub, "replace "):
		g, err := s.ReplaceGoal(strings.TrimSpace(args[len("replace "):]), 0)
		if err != nil {
			return "/goal replace: " + err.Error(), GoalStartNone
		}
		return "Goal replaced — " + goalObjectiveLine(g.Objective), GoalStartFresh

	default:
		// "/goal <objective>": start a goal. A finished goal is replaced
		// without ceremony; an unfinished one needs the explicit replace
		// subcommand so a typo can't discard live work (TUI parity).
		if g, ok := s.GoalSnapshot(); ok {
			if g.Status != GoalComplete {
				return "A goal already exists (" + GoalStatusLabel(g.Status) + "): " + goalObjectiveLine(g.Objective) +
					"\nUse /goal replace <objective> to replace it, or /goal clear.", GoalStartNone
			}
			ng, err := s.ReplaceGoal(args, 0)
			if err != nil {
				return "/goal: " + err.Error(), GoalStartNone
			}
			return "Goal set — " + goalObjectiveLine(ng.Objective), GoalStartFresh
		}
		g, err := s.CreateGoal(args, 0)
		if err != nil {
			return "/goal: " + err.Error(), GoalStartNone
		}
		return "Goal set — " + goalObjectiveLine(g.Objective), GoalStartFresh
	}
}

// GoalStatusLabel is the human-readable status name shared by every surface.
func GoalStatusLabel(status GoalStatus) string {
	switch status {
	case GoalActive:
		return "active"
	case GoalPaused:
		return "paused"
	case GoalBlocked:
		return "blocked"
	case GoalUsageLimited:
		return "usage limited"
	case GoalBudgetLimited:
		return "limited by budget"
	case GoalComplete:
		return "complete"
	}
	return string(status)
}

// FormatGoalTokens renders a token count compactly: 950, 12.5K, 1.2M.
func FormatGoalTokens(n int64) string {
	format := func(v float64, suffix string) string {
		s := fmt.Sprintf("%.1f", v)
		s = strings.TrimSuffix(s, ".0")
		return s + suffix
	}
	switch {
	case n >= 1_000_000:
		return format(float64(n)/1_000_000, "M")
	case n >= 1_000:
		return format(float64(n)/1_000, "K")
	default:
		return fmt.Sprintf("%d", n)
	}
}

// FormatGoalElapsed renders whole seconds compactly: 45s, 12m, 1h 30m, 2d 3h 5m.
func FormatGoalElapsed(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	remMin := minutes % 60
	if hours >= 24 {
		return fmt.Sprintf("%dd %dh %dm", hours/24, hours%24, remMin)
	}
	if remMin == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh %dm", hours, remMin)
}

// FormatElapsedSeconds renders whole seconds always down to the second: 45s,
// 12m30s. Unlike FormatGoalElapsed (which drops the remainder once minutes
// take over, since goal budgets run long), a single turn is short enough that
// dropping the seconds reads as suspiciously round — this mirrors the web
// frontend's fmtDur exactly so the per-turn summary line looks identical
// across the CLI, Web, and IM surfaces.
func FormatElapsedSeconds(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	return fmt.Sprintf("%dm%ds", seconds/60, seconds%60)
}

// CacheUtilizationPct returns the share of a turn's prompt tokens that were
// served from the provider's prompt cache: read / (input + read + write).
// InputTokens and CacheRead/WriteTokens are non-overlapping buckets (see
// accrueUsage), so the denominator is the whole prompt sent. ok is false when
// the backend reported no cache activity at all — callers omit the readout
// then instead of rendering a misleading "cache 0%". A warming turn (write
// only, read 0) does report 0%, which is honest: the cache exists but nothing
// was served from it yet.
func CacheUtilizationPct(inputTokens, cacheRead, cacheWrite int) (pct int, ok bool) {
	total := inputTokens + cacheRead + cacheWrite
	if cacheRead+cacheWrite <= 0 || total <= 0 {
		return 0, false
	}
	return cacheRead * 100 / total, true
}

// GoalUsageLine summarizes a goal's spend: "12m, 63.9K/50K tokens".
func GoalUsageLine(g Goal) string {
	var parts []string
	if g.TimeUsedSeconds > 0 {
		parts = append(parts, FormatGoalElapsed(g.TimeUsedSeconds))
	}
	if g.TokenBudget > 0 {
		parts = append(parts, FormatGoalTokens(g.TokensUsed)+"/"+FormatGoalTokens(g.TokenBudget)+" tokens")
	} else if g.TokensUsed > 0 {
		parts = append(parts, FormatGoalTokens(g.TokensUsed)+" tokens")
	}
	if len(parts) == 0 {
		return "no usage recorded"
	}
	return strings.Join(parts, ", ")
}

func goalCommandSummary(g Goal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Goal %s: %s\n", GoalStatusLabel(g.Status), goalObjectiveLine(g.Objective))
	fmt.Fprintf(&b, "Time used: %s · Tokens used: %s", FormatGoalElapsed(g.TimeUsedSeconds), FormatGoalTokens(g.TokensUsed))
	if g.TokenBudget > 0 {
		fmt.Fprintf(&b, " / %s budget", FormatGoalTokens(g.TokenBudget))
	}
	b.WriteString("\n")
	switch g.Status {
	case GoalActive:
		b.WriteString("Commands: /goal edit <objective> · /goal pause · /goal clear")
	case GoalPaused, GoalBlocked, GoalUsageLimited:
		b.WriteString("Commands: /goal edit <objective> · /goal resume · /goal clear")
	default:
		b.WriteString("Commands: /goal edit <objective> · /goal clear")
	}
	return b.String()
}

// goalObjectiveLine clips an objective to one reply-friendly line.
func goalObjectiveLine(objective string) string {
	line := objective
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if r := []rune(line); len(r) > 80 {
		return strings.TrimSpace(string(r[:79])) + "…"
	}
	return line
}
