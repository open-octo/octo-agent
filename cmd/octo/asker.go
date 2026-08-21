package main

import (
	"context"

	"github.com/open-octo/octo-agent/internal/tools"
)

// replAsker adapts the ask_user_question tool's tools.Asker contract onto the
// view's structured prompt seam (userPrompter). The view owns presentation —
// the plain-text card on stdin, or a TUI modal — so this adapter just
// translates AskRequest ↔ UserPrompt and UserResponse ↔ AskResponse.
type replAsker struct {
	ask userPrompter
}

func newREPLAsker(ask userPrompter) *replAsker {
	return &replAsker{ask: ask}
}

// Ask implements tools.Asker.
func (a *replAsker) Ask(ctx context.Context, q tools.AskRequest) (tools.AskResponse, error) {
	if a.ask == nil {
		return tools.AskResponse{Outcome: tools.AskRejected}, nil
	}
	questions := make([]UserQuestion, 0, len(q.Questions))
	for i, question := range q.Questions {
		opts := make([]UserOption, 0, len(question.Options))
		for _, opt := range question.Options {
			opts = append(opts, UserOption{
				Label:       opt.Label,
				Description: opt.Description,
				Preview:     opt.Preview,
			})
		}
		questions = append(questions, UserQuestion{
			Question:    question.Question,
			Header:      question.HeaderOrDefault(i),
			MultiSelect: question.MultiSelect,
			Options:     opts,
		})
	}
	resp, err := a.ask.Ask(ctx, UserPrompt{Kind: KindQuestion, Questions: questions})
	if err != nil {
		return tools.AskResponse{}, err
	}
	res := tools.AskResponse{Outcome: askOutcome(resp.Outcome)}
	for _, ans := range resp.Answers {
		res.Answers = append(res.Answers, tools.AskAnswer{
			Choices: ans.Choices,
			Custom:  ans.Custom,
			Notes:   ans.Notes,
		})
	}
	return res, nil
}

// askOutcome maps the view's outcome onto the tool contract's.
func askOutcome(o UserAskOutcome) tools.AskOutcome {
	switch o {
	case PromptClarify:
		return tools.AskClarify
	case PromptRejected:
		return tools.AskRejected
	default:
		return tools.AskSubmitted
	}
}

// AskSecret implements tools.SecretAsker: the TUI/REPL collects secrets too —
// masked input in the bubbletea modal, a no-echo read in the plain view. The
// answer returns to the runtime caller only; it never becomes a tool result.
func (a *replAsker) AskSecret(ctx context.Context, question string) (string, bool, error) {
	if a.ask == nil {
		return "", true, nil
	}
	resp, err := a.ask.Ask(ctx, UserPrompt{Kind: KindSecret, Question: question})
	if err != nil {
		return "", false, err
	}
	if resp.Cancelled {
		return "", true, nil
	}
	return resp.Custom, false, nil
}
