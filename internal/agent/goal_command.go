package agent

import (
	"fmt"
	"strings"
)

// GoalCommandUsage is the one-line grammar hint shared by the text-reply
// transports (web slash command, IM command).
const GoalCommandUsage = "Usage: /goal <objective> · /goal edit <objective> · /goal pause|resume|clear · /goal replace <objective>"

// GoalCommand applies a "/goal …" command to the session and returns a plain
// text reply, the shared surface for transports whose command feedback is a
// text message (web toast, IM chat reply). The grammar matches the TUI except
// `edit`, which takes the new objective inline — these transports have no
// input-prefill to hand the current objective back for editing.
//
// The TUI keeps its own richer dispatcher (prefilled edit, styled summary);
// the semantics here and there must stay aligned.
func GoalCommand(s *Session, args string) string {
	args = strings.TrimSpace(args)
	sub := strings.ToLower(args)

	switch {
	case args == "":
		g, ok := s.GoalSnapshot()
		if !ok {
			return "No goal is currently set. " + GoalCommandUsage
		}
		return goalCommandSummary(g)

	case sub == "pause":
		g, err := s.SetGoalStatus(GoalPaused)
		if err != nil {
			return "/goal pause: " + err.Error()
		}
		return "Goal " + GoalStatusLabel(g.Status) + " — " + goalObjectiveLine(g.Objective)

	case sub == "resume":
		g, err := s.SetGoalStatus(GoalActive)
		if err != nil {
			return "/goal resume: " + err.Error()
		}
		return "Goal " + GoalStatusLabel(g.Status) + " — " + goalObjectiveLine(g.Objective)

	case sub == "clear":
		if s.ClearGoal() {
			return "Goal cleared"
		}
		return "No goal to clear"

	case sub == "edit":
		return "Usage: /goal edit <objective> — rewrites the objective, keeping usage and budget"

	case strings.HasPrefix(sub, "edit "):
		g, err := s.EditGoalObjective(strings.TrimSpace(args[len("edit "):]))
		if err != nil {
			return "/goal edit: " + err.Error()
		}
		return "Goal updated — " + goalObjectiveLine(g.Objective)

	case sub == "replace":
		return "Usage: /goal replace <objective>"

	case strings.HasPrefix(sub, "replace "):
		g, err := s.ReplaceGoal(strings.TrimSpace(args[len("replace "):]), 0)
		if err != nil {
			return "/goal replace: " + err.Error()
		}
		return "Goal replaced — " + goalObjectiveLine(g.Objective)

	default:
		// "/goal <objective>": start a goal. A finished goal is replaced
		// without ceremony; an unfinished one needs the explicit replace
		// subcommand so a typo can't discard live work (TUI parity).
		if g, ok := s.GoalSnapshot(); ok {
			if g.Status != GoalComplete {
				return "A goal already exists (" + GoalStatusLabel(g.Status) + "): " + goalObjectiveLine(g.Objective) +
					"\nUse /goal replace <objective> to replace it, or /goal clear."
			}
			ng, err := s.ReplaceGoal(args, 0)
			if err != nil {
				return "/goal: " + err.Error()
			}
			return "Goal set — " + goalObjectiveLine(ng.Objective)
		}
		g, err := s.CreateGoal(args, 0)
		if err != nil {
			return "/goal: " + err.Error()
		}
		return "Goal set — " + goalObjectiveLine(g.Objective)
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
