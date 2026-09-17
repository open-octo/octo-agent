package tools

import (
	"strings"
	"testing"
)

// TestFormatSubAgentNote_MaxTurnsFlagged verifies an async sub-agent that hit
// its turn limit is flagged INCOMPLETE in the completion notice, so the parent
// doesn't treat partial work as a finished answer.
func TestFormatSubAgentNote_MaxTurnsFlagged(t *testing.T) {
	got := FormatSubAgentNote(SubAgentNotification{
		AgentID:     "agent_1",
		Description: "investigate",
		Kind:        "spawn_done",
		Result:      "partial findings",
		StopReason:  "max_turns",
	})
	if !strings.Contains(got, "INCOMPLETE") {
		t.Errorf("max_turns notice should be flagged INCOMPLETE, got:\n%s", got)
	}

	// Normal completion is not flagged.
	clean := FormatSubAgentNote(SubAgentNotification{
		AgentID: "agent_2", Description: "x", Kind: "spawn_done", Result: "done",
	})
	if strings.Contains(clean, "INCOMPLETE") {
		t.Errorf("a complete notice should not be flagged, got:\n%s", clean)
	}
}

// A background sub-agent stopped by the loop detector must be flagged too —
// the result arriving by notification rather than inline changes nothing about
// how partial it is.
func TestFormatSubAgentNote_StuckFlagged(t *testing.T) {
	got := FormatSubAgentNote(SubAgentNotification{
		AgentID:     "agent_3",
		Description: "investigate",
		Kind:        "spawn_done",
		Result:      "got this far",
		StopReason:  "stuck",
	})
	if !strings.Contains(got, "INCOMPLETE") {
		t.Errorf("a stuck notice should be flagged INCOMPLETE, got:\n%s", got)
	}
	if !strings.Contains(got, "agent_3") {
		t.Errorf("the note should name the resume handle, got:\n%s", got)
	}
}
