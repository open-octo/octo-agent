package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/open-octo/octo-agent/internal/agent"
)

// goalsWired reports whether the session-goal feature is wired for this TUI
// session (chat.go sets the accountant only when goal.enabled and the session
// persists).
func (m *tuiModel) goalsWired() bool { return m.a.GoalAcct != nil }

const goalUsage = "Usage: /goal <objective> · /goal edit|pause|resume|clear · /goal replace <objective>"

// dispatchGoal handles "/goal [...]" when the session is idle.
func (m *tuiModel) dispatchGoal(args string) (tea.Model, tea.Cmd) {
	if !m.goalsWired() {
		m.println(noticeStyle.Render("/goal: goals are disabled (goal.enabled) or unavailable in this session"))
		return m, nil
	}
	sess := m.cfg.session
	args = strings.TrimSpace(args)
	sub := strings.ToLower(args)

	switch {
	case args == "":
		g, ok := sess.GoalSnapshot()
		if !ok {
			m.println(noticeStyle.Render(goalUsage))
			m.println(noticeStyle.Render("No goal is currently set. Example: /goal improve benchmark coverage"))
			return m, nil
		}
		m.printlnBlock(goalSummary(g))
		return m, nil

	case sub == "pause":
		return m.applyGoalStatus(agent.GoalPaused)

	case sub == "resume":
		model, cmd := m.applyGoalStatus(agent.GoalActive)
		// Resuming re-enters the continuation loop right away when idle —
		// the user just asked for the goal to keep going.
		if g, ok := sess.GoalSnapshot(); ok && g.Status == agent.GoalActive {
			if prompt, kick := m.goalContinuationKick(); kick {
				return model, tea.Sequence(m.flushPrints(), m.startTurnEcho(prompt, ""))
			}
		}
		return model, cmd

	case sub == "clear":
		if sess.ClearGoal() {
			m.println(noticeStyle.Render("Goal cleared"))
		} else {
			m.println(noticeStyle.Render("No goal to clear"))
		}
		return m, nil

	case sub == "edit":
		g, ok := sess.GoalSnapshot()
		if !ok {
			m.println(noticeStyle.Render("No goal is currently set. Create one first: /goal <objective>"))
			return m, nil
		}
		// Next submitted line becomes the new objective (usage counters and
		// budget survive an edit). Esc or an empty submit cancels.
		m.goalEditPending = true
		m.ta.SetValue(g.Objective)
		m.println(noticeStyle.Render("Editing goal — change the objective and press Enter (Esc to cancel)"))
		return m, nil

	case strings.HasPrefix(sub, "replace "):
		objective := strings.TrimSpace(args[len("replace "):])
		g, err := sess.ReplaceGoal(objective, 0)
		if err != nil {
			m.println(errorStyle.Render("/goal replace: " + err.Error()))
			return m, nil
		}
		m.println(noticeStyle.Render("Goal replaced — " + goalOneLine(g)))
		return m, nil

	default:
		// "/goal <objective>": start a goal. A finished (complete) goal is
		// replaced silently; an unfinished one needs the explicit replace
		// subcommand so a typo can't discard live work.
		if g, ok := sess.GoalSnapshot(); ok {
			if g.Status != agent.GoalComplete {
				m.printlnBlock(goalSummary(g))
				m.println(noticeStyle.Render("A goal already exists — /goal replace <objective> to replace it, or /goal clear"))
				return m, nil
			}
			if ng, err := sess.ReplaceGoal(args, 0); err != nil {
				m.println(errorStyle.Render("/goal: " + err.Error()))
			} else {
				m.println(noticeStyle.Render("Goal set — " + goalOneLine(ng)))
			}
			return m, nil
		}
		g, err := sess.CreateGoal(args, 0)
		if err != nil {
			m.println(errorStyle.Render("/goal: " + err.Error()))
			return m, nil
		}
		m.println(noticeStyle.Render("Goal set — " + goalOneLine(g)))
		return m, nil
	}
}

// applyGoalStatus routes a user pause/resume through the session and reports
// the outcome (which may differ from the request — resuming an over-budget
// goal lands on budget_limited).
func (m *tuiModel) applyGoalStatus(status agent.GoalStatus) (tea.Model, tea.Cmd) {
	g, err := m.cfg.session.SetGoalStatus(status)
	if err != nil {
		m.println(errorStyle.Render("/goal: " + err.Error()))
		return m, nil
	}
	m.println(noticeStyle.Render("Goal " + goalStatusLabel(g.Status) + " — " + goalOneLine(g)))
	return m, nil
}

// submitGoalEdit consumes the next submitted line as the edited objective.
func (m *tuiModel) submitGoalEdit(text string) (tea.Model, tea.Cmd) {
	m.goalEditPending = false
	if text == "" {
		m.println(noticeStyle.Render("Goal edit cancelled"))
		return m, nil
	}
	g, err := m.cfg.session.EditGoalObjective(text)
	if err != nil {
		m.println(errorStyle.Render("/goal edit: " + err.Error()))
		return m, nil
	}
	m.println(noticeStyle.Render("Goal updated — " + goalOneLine(g)))
	return m, nil
}

// goalContinuationKick asks the session whether an idle continuation turn
// should start. Split from the call sites (turn end, /goal resume) so both
// share the notice.
func (m *tuiModel) goalContinuationKick() (string, bool) {
	if !m.goalsWired() {
		return "", false
	}
	prompt, ok := m.cfg.session.GoalContinuation()
	if !ok {
		return "", false
	}
	m.printlnBlock(noticeStyle.Render("● Goal continues — /goal pause to stop"))
	return prompt, true
}

// handleGoalUpdated reacts to in-turn accounting events: the status segment
// re-renders from the session on every frame, so only status *transitions*
// need a scrollback notice.
func (m *tuiModel) handleGoalUpdated(g *agent.Goal) {
	if g == nil {
		return
	}
	prev := m.goalLastStatus
	m.goalLastStatus = g.Status
	if prev == g.Status || prev == "" {
		return
	}
	switch g.Status {
	case agent.GoalBudgetLimited:
		m.printlnBlock(noticeStyle.Render(fmt.Sprintf("● Goal budget reached (%s/%s tokens) — wrapping up",
			compactCount(g.TokensUsed), compactCount(g.TokenBudget))))
	case agent.GoalComplete:
		m.printlnBlock(noticeStyle.Render("● Goal complete — " + goalUsageSummary(g)))
	case agent.GoalBlocked:
		m.printlnBlock(noticeStyle.Render("● Goal blocked — the agent is at an impasse; /goal resume to retry"))
	}
}

// goalStartupNotice returns the line printed under the banner when a resumed
// session carries a goal that isn't running — the Codex resume-paused prompt,
// as a hint instead of a modal.
func goalStartupNotice(sess *agent.Session) string {
	g, ok := sess.GoalSnapshot()
	if !ok {
		return ""
	}
	switch g.Status {
	case agent.GoalPaused, agent.GoalBlocked, agent.GoalUsageLimited:
		return noticeStyle.Render("● Goal " + goalStatusLabel(g.Status) + ": " + goalTitleLine(g.Objective) +
			" — /goal resume to continue")
	case agent.GoalActive:
		return noticeStyle.Render("● Goal active: " + goalTitleLine(g.Objective) + " — continues after your next message")
	}
	return ""
}

// ─── formatting helpers ─────────────────────────────────────────────────────

func goalStatusLabel(status agent.GoalStatus) string {
	switch status {
	case agent.GoalActive:
		return "active"
	case agent.GoalPaused:
		return "paused"
	case agent.GoalBlocked:
		return "blocked"
	case agent.GoalUsageLimited:
		return "usage limited"
	case agent.GoalBudgetLimited:
		return "limited by budget"
	case agent.GoalComplete:
		return "complete"
	}
	return string(status)
}

// goalStatusSegment is the compact status-bar value: usage while active,
// the status label otherwise.
func goalStatusSegment(g agent.Goal) string {
	switch g.Status {
	case agent.GoalActive:
		if g.TokenBudget > 0 {
			return compactCount(g.TokensUsed) + "/" + compactCount(g.TokenBudget)
		}
		return goalElapsed(g.TimeUsedSeconds)
	case agent.GoalBudgetLimited:
		if g.TokenBudget > 0 {
			return "budget " + compactCount(g.TokensUsed) + "/" + compactCount(g.TokenBudget)
		}
	}
	return goalStatusLabel(g.Status)
}

func goalSummary(g agent.Goal) string {
	var b strings.Builder
	b.WriteString("Goal\n")
	fmt.Fprintf(&b, "  Status:      %s\n", goalStatusLabel(g.Status))
	fmt.Fprintf(&b, "  Objective:   %s\n", g.Objective)
	fmt.Fprintf(&b, "  Time used:   %s\n", goalElapsed(g.TimeUsedSeconds))
	fmt.Fprintf(&b, "  Tokens used: %s", compactCount(g.TokensUsed))
	if g.TokenBudget > 0 {
		fmt.Fprintf(&b, "\n  Budget:      %s (%s remaining)", compactCount(g.TokenBudget), compactCount(g.RemainingTokens()))
	}
	b.WriteString("\n\n  ")
	switch g.Status {
	case agent.GoalActive:
		b.WriteString("Commands: /goal edit · /goal pause · /goal clear")
	case agent.GoalPaused, agent.GoalBlocked, agent.GoalUsageLimited:
		b.WriteString("Commands: /goal edit · /goal resume · /goal clear")
	default:
		b.WriteString("Commands: /goal edit · /goal clear")
	}
	return b.String()
}

func goalOneLine(g agent.Goal) string {
	s := goalTitleLine(g.Objective)
	if g.TokenBudget > 0 {
		s += fmt.Sprintf(" (budget %s tokens)", compactCount(g.TokenBudget))
	}
	return s
}

func goalUsageSummary(g *agent.Goal) string {
	parts := []string{}
	if g.TimeUsedSeconds > 0 {
		parts = append(parts, goalElapsed(g.TimeUsedSeconds))
	}
	if g.TokenBudget > 0 {
		parts = append(parts, compactCount(g.TokensUsed)+"/"+compactCount(g.TokenBudget)+" tokens")
	} else if g.TokensUsed > 0 {
		parts = append(parts, compactCount(g.TokensUsed)+" tokens")
	}
	if len(parts) == 0 {
		return "no usage recorded"
	}
	return strings.Join(parts, ", ")
}

// goalTitleLine clips an objective to one status-line-friendly line.
func goalTitleLine(objective string) string {
	line := objective
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if r := []rune(line); len(r) > 60 {
		return strings.TrimSpace(string(r[:59])) + "…"
	}
	return line
}

// goalElapsed renders whole seconds compactly: 45s, 12m, 1h 30m, 2d 3h 5m.
func goalElapsed(seconds int64) string {
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

// compactCount renders a token count compactly: 950, 12.5K, 1.2M.
func compactCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return trimZero(fmt.Sprintf("%.1fM", float64(n)/1_000_000))
	case n >= 1_000:
		return trimZero(fmt.Sprintf("%.1fK", float64(n)/1_000))
	default:
		return fmt.Sprintf("%d", n)
	}
}

func trimZero(s string) string {
	return strings.Replace(s, ".0", "", 1)
}
