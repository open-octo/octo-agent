package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/open-octo/octo-agent/internal/tasks"
)

// PendingTaskReminder is the agent's TurnEndReminder for the task checklist: it
// catches a plan whose last steps are still marked in_progress at the moment
// the model stops talking. The model is told to "update status as you go", but
// after a long tool-heavy turn it routinely reports "all done" in prose and
// forgets the closing task_update — leaving the plan panel showing "1/2 done"
// against a reply that says everything is finished.
//
// Two conditions keep it from billing round-trips it hasn't earned:
//
//   - The turn must have touched the plan itself. An unfinished plan survives
//     across turns by design (only a fully-completed one is cleared), and the
//     reminder explicitly allows the model to answer "that task really isn't
//     done". Without this check, one task the model refuses to close would tax
//     every later turn in the session — including turns about something else
//     entirely. It also keeps the reminder from naming task_update at a turn
//     where the active profile never advertised it.
//   - Nothing may be left pending. An in_progress task with work still queued
//     behind it is a plan the model is mid-way through, not one it is closing.
//
// Neither condition can tell "finished the last step but forgot to file it"
// (the bug) from "stopped on the last step to ask the user something" (fine) —
// nothing observable separates those, so the second case costs one extra
// round-trip. The reminder is worded to keep that case cheap: acknowledge and
// stop, don't restate the question.
//
// Returns "" when there is nothing to remind about, which ends the turn
// normally. The reminder is a <system-reminder>, so no UI renders it.
func PendingTaskReminder(ctx context.Context, toolsUsed []string) string {
	if !touchedPlan(toolsUsed) {
		return ""
	}
	return pendingTaskReminder(resolveTaskStore(ctx))
}

// touchedPlan reports whether any task_* tool ran during the turn.
func touchedPlan(toolsUsed []string) bool {
	for _, name := range toolsUsed {
		if strings.HasPrefix(name, "task_") {
			return true
		}
	}
	return false
}

func pendingTaskReminder(store TaskStore) string {
	if store == nil {
		return ""
	}
	var inProgress []tasks.Task
	for _, t := range store.List() {
		switch t.Status {
		case tasks.Pending:
			return "" // still work queued — the plan is mid-flight, not closing
		case tasks.InProgress:
			inProgress = append(inProgress, t)
		}
	}
	if len(inProgress) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<system-reminder>\n")
	b.WriteString("Your turn is about to end with these tasks still marked in_progress:\n")
	for _, t := range inProgress {
		fmt.Fprintf(&b, "  #%d %s\n", t.ID, t.Subject)
	}
	b.WriteString("If you finished the work, call task_update to mark each one completed " +
		"(status `deleted` if it no longer applies) so the plan the user sees matches what " +
		"you just reported. If a task genuinely isn't done, leave it as is. Either way this " +
		"is bookkeeping, not a new question for the user — do not repeat your answer; reply " +
		"with at most one short line.\n")
	b.WriteString("</system-reminder>")
	return b.String()
}
