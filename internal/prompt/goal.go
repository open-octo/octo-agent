package prompt

import (
	"embed"
	"strconv"
	"strings"
	"text/template"
)

// The goal steering prompts are ported 1:1 from Codex (codex-rs
// core/templates/goals), with the plan-tool paragraph reworded to octo's task
// tools. They are runtime-owned model steering, not user speech: callers wrap
// the rendered text in <goal_context> markers and inject it as a hidden user
// message.
//
//go:embed goals/continuation.md goals/budget_limit.md goals/objective_updated.md
var goalTemplatesFS embed.FS

var goalTemplates = template.Must(template.ParseFS(goalTemplatesFS,
	"goals/continuation.md", "goals/budget_limit.md", "goals/objective_updated.md"))

// GoalPromptData carries the goal fields the steering templates render.
// Objective is raw user text — rendering XML-escapes it so a crafted
// objective cannot break out of its <objective> delimiters.
type GoalPromptData struct {
	Objective       string
	TokensUsed      int64
	TokenBudget     int64 // 0 = unbudgeted
	TimeUsedSeconds int64
}

// goalTemplateView is the pre-formatted shape the templates consume: budget
// fields become "none"/"unbounded" when the goal has no token budget.
type goalTemplateView struct {
	Objective       string
	TokensUsed      int64
	TokenBudget     string
	RemainingTokens string
	TimeUsedSeconds int64
}

func (d GoalPromptData) view() goalTemplateView {
	v := goalTemplateView{
		Objective:       escapeXMLText(d.Objective),
		TokensUsed:      d.TokensUsed,
		TokenBudget:     "none",
		RemainingTokens: "unbounded",
		TimeUsedSeconds: d.TimeUsedSeconds,
	}
	if d.TokenBudget > 0 {
		v.TokenBudget = strconv.FormatInt(d.TokenBudget, 10)
		v.RemainingTokens = strconv.FormatInt(max(d.TokenBudget-d.TokensUsed, 0), 10)
	}
	return v
}

// GoalContinuation renders the hidden prompt that keeps an active goal
// moving after a turn completes: keep the full objective, work from current
// evidence, and only mark the goal complete after a requirement-by-
// requirement audit (or blocked after the strict three-turn audit).
func GoalContinuation(d GoalPromptData) string { return renderGoal("continuation.md", d) }

// GoalBudgetLimit renders the one-time wrap-up steer injected when the goal
// crosses its token budget.
func GoalBudgetLimit(d GoalPromptData) string { return renderGoal("budget_limit.md", d) }

// GoalObjectiveUpdated renders the steer injected when the user edits the
// objective while a turn is running.
func GoalObjectiveUpdated(d GoalPromptData) string { return renderGoal("objective_updated.md", d) }

func renderGoal(name string, d GoalPromptData) string {
	var sb strings.Builder
	if err := goalTemplates.ExecuteTemplate(&sb, name, d.view()); err != nil {
		// The templates are embedded and parsed at init; execution over a
		// plain struct cannot fail in practice.
		panic("prompt: goal template " + name + ": " + err.Error())
	}
	// A Windows checkout embeds the .md templates with CRLF (no
	// .gitattributes pins LF); normalize so the model-facing prompt — and
	// the exact-match tests — are identical on every platform.
	out := strings.ReplaceAll(sb.String(), "\r\n", "\n")
	return strings.TrimRight(out, "\n")
}

func escapeXMLText(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}
