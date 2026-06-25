package tools

import (
	"fmt"
	"strings"
)

// FormatWorkflowNote renders a background workflow completion as a
// <system-reminder> note for the model, mirroring FormatSubAgentNote. It lets
// the transport nudge the model when a detached run finishes instead of
// relying on the model to poll workflow_status.
func FormatWorkflowNote(ev WorkflowNotification) string {
	var b strings.Builder
	b.WriteString("<system-reminder>\n")
	b.WriteString("[BACKGROUND COMPLETED]\n")
	desc := ev.Description
	if desc == "" {
		desc = "workflow"
	}
	if ev.Status == "error" {
		fmt.Fprintf(&b, "Workflow %s (%s) failed.", ev.RunID, desc)
	} else {
		fmt.Fprintf(&b, "Workflow %s (%s) has completed.", ev.RunID, desc)
	}
	if ev.Result != "" {
		b.WriteString("\nResult:\n")
		b.WriteString(ev.Result)
	}
	if ev.JournalRunID != "" {
		fmt.Fprintf(&b, "\n[workflow run: %s]", ev.JournalRunID)
	}
	fmt.Fprintf(&b, "\nUse workflow_status(%q) for the full result.", ev.RunID)
	b.WriteString("\n</system-reminder>")
	return b.String()
}
