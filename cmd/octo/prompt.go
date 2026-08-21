package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// UserPrompt is a structured request for a synchronous answer from the user,
// raised mid-turn from the agent goroutine. The two kinds — a tool-permission
// approval and an ask_user_question selection — share one path so the view
// (plain stdin today, a bubbletea modal next) renders and answers them
// uniformly. See dev-docs/tui-input-modes-design.md §6.
type UserPrompt struct {
	Kind UserPromptKind

	// Permission (KindPermission): the tool the agent wants to run.
	ToolName  string
	ToolInput map[string]any

	// Question (KindQuestion): the ask_user_question payload — 1..4 questions
	// answered in one prompt.
	Questions []UserQuestion

	// Question (KindSecret): the single prompt line for a masked read.
	Question string
}

// UserQuestion is one question of an ask_user_question set.
type UserQuestion struct {
	Question    string
	Header      string // labels this question; never empty (see HeaderOrDefault)
	MultiSelect bool
	Options     []UserOption
}

// UserOption is one choice. Label and Description stay separate so the view
// can render the label prominently with its description beneath; a non-empty
// Preview puts the question in the two-column preview layout.
type UserOption struct {
	Label       string
	Description string
	Preview     string
}

// UserAnswer is one question's answer. Custom is the "Other" free text;
// Notes is a note attached in the preview layout.
type UserAnswer struct {
	Choices []string
	Custom  string
	Notes   string
}

// UserAskOutcome mirrors tools.AskOutcome for the view layer: the user either
// submitted the set, asked to talk it over instead, or dismissed it.
type UserAskOutcome int

const (
	// PromptSubmitted: answers (possibly partial) were submitted.
	PromptSubmitted UserAskOutcome = iota
	// PromptClarify: "Chat about this" — resolve now, let the model ask.
	PromptClarify
	// PromptRejected: dismissed; answers are discarded.
	PromptRejected
)

// UserPromptKind tags a UserPrompt.
type UserPromptKind int

const (
	// KindPermission asks the user to approve/deny a tool call.
	KindPermission UserPromptKind = iota
	// KindQuestion asks the user to pick option(s) or supply free text.
	KindQuestion
	// KindSecret asks for a secret value with masked input (no echo, no
	// options). The answer travels only in the UserResponse.Custom channel
	// back to the runtime caller — the view must never render it.
	KindSecret
)

// UserResponse is the structured answer to a UserPrompt.
type UserResponse struct {
	// Permission (KindPermission):
	Allow  bool // run this tool call
	Always bool // ...and remember the allow for the rest of the session

	// Question (KindQuestion): one entry per question asked.
	Outcome UserAskOutcome
	Answers []UserAnswer

	// Secret (KindSecret): the value read with echo off. Also carries the
	// answer for the single-question secret prompt.
	Custom string

	// Cancelled reports a dismissed secret prompt.
	Cancelled bool
}

// userPrompter is the narrow capability the permission gate and the
// ask_user_question asker need from a view: raise a structured prompt and
// block for the answer. Both plainView and the bubbletea view satisfy it, so
// the gate/asker stay ignorant of how the answer is gathered (stdin line vs
// modal). It is exactly ViewSink's Ask method, split out so callers depend
// only on what they use.
type userPrompter interface {
	Ask(ctx context.Context, p UserPrompt) (UserResponse, error)
}

// parseSelection converts the user's typed answer into a list of 1-based
// option indices. Empty / non-numeric input returns nil with no error;
// genuinely malformed input (e.g. mixing numbers and prose) errors so the
// caller can treat it as cancellation rather than guessing.
//
// In single-select mode, only the first index is honored even if the user
// typed multiple.
func parseSelection(raw string, maxIdx int, multi bool) ([]int, error) {
	parts := strings.Split(raw, ",")
	var out []int
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("not a number: %q", p)
		}
		if n < 1 || n > maxIdx {
			return nil, fmt.Errorf("out of range: %d", n)
		}
		out = append(out, n)
	}
	if !multi && len(out) > 1 {
		out = out[:1]
	}
	return out, nil
}
