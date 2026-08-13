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
// It fires only when nothing is left pending: an in_progress task with pending
// work queued behind it is a plan the model is legitimately in the middle of
// (it commonly stops there to ask the user something), and nagging about it
// would cost an extra round-trip on every such turn. With the queue empty, the
// model believes it is on the final step — ending the turn there is exactly
// the case this guard exists for.
//
// Returns "" when there is nothing to remind about, which ends the turn
// normally. The reminder is a <system-reminder>, so no UI renders it.
func PendingTaskReminder(ctx context.Context) string {
	return pendingTaskReminder(resolveTaskStore(ctx))
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
