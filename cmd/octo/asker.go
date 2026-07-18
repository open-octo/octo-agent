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
		return tools.AskResponse{Cancelled: true}, nil
	}
	resp, err := a.ask.Ask(ctx, UserPrompt{
		Kind:        KindQuestion,
		Header:      q.Header,
		Question:    q.Question,
		Options:     q.Options,
		MultiSelect: q.MultiSelect,
	})
	if err != nil {
		return tools.AskResponse{}, err
	}
	return tools.AskResponse{
		Choices:   resp.Choices,
		Custom:    resp.Custom,
		Cancelled: resp.Cancelled,
	}, nil
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
