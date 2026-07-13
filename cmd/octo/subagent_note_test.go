package main

import "testing"

func TestSubAgentNoteStatus(t *testing.T) {
	cases := []struct {
		stopReason, want string
	}{
		{"", "failed"},                            // error exit
		{"end_turn", "completed"},                 // normal completion
		{"tool_use", "completed"},                 // normal completion
		{"stop", "completed"},                     // OpenAI-protocol normal completion
		{"max_turns", "incomplete (max_turns)"},   // turn-budget checkpoint
		{"max_tokens", "incomplete (max_tokens)"}, // output-budget checkpoint
	}
	for _, c := range cases {
		if got := subAgentNoteStatus(c.stopReason); got != c.want {
			t.Errorf("subAgentNoteStatus(%q) = %q, want %q", c.stopReason, got, c.want)
		}
	}
}
