package tools

import (
	"context"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/hooks"
)

// InterfaceNote tells the model, on every user turn, which interface its
// reply will be read in. What renders differs sharply — the Web UI draws
// ```mermaid fences and GenUI panels, an IM chat and the terminal show them
// as source — and nothing else in the context says where the user is.
//
// It rides the user message rather than the system prompt for two reasons.
// A session can change interface mid-conversation (the Web UI can take over
// a session an IM chat was driving, and the reverse), which a frozen prompt
// could not follow; and rewriting the prompt to follow it would break the
// provider's cached prefix. Appending to the new user message leaves every
// earlier byte untouched.
//
// It fires every turn rather than only on a change: the Web UI and an IM
// chat each drive a session through their own Agent over one shared history,
// so neither can know from its own state what the other last told the model.
type InterfaceNote struct{}

// NewInterfaceNote returns a ready note.
func NewInterfaceNote() *InterfaceNote { return &InterfaceNote{} }

// RegisterHooks wires the note onto a session's hook engine for
// UserPromptSubmit. The interface comes from the payload's Transport, which
// every turn-starting call site sets to the entry actually running the turn.
func (n *InterfaceNote) RegisterHooks(e *hooks.Engine) {
	if n == nil || e == nil {
		return
	}
	e.RegisterInProc(hooks.EventUserPromptSubmit, func(_ context.Context, p hooks.Payload) string {
		return InterfaceReminder(p.Transport)
	})
}

// InterfaceReminder returns the reminder for an entry, or "" for an entry
// whose rendering is unknown (the prompt then tells the model to prefer
// markdown).
func InterfaceReminder(transport string) string {
	var line string
	switch transport {
	case agent.EntryWeb:
		line = "Interface: the Web UI. Markdown, ```mermaid fences and GenUI panels all render here."
	case agent.EntryChannel:
		line = "Interface: an IM chat. Only markdown renders; ```mermaid fences and GenUI show as source, so write a flow as a numbered list."
	case agent.EntryTUI:
		line = "Interface: the terminal UI. Only markdown renders; ```mermaid fences and GenUI show as source, so write a flow as a numbered list."
	case agent.EntryCLI:
		line = "Interface: a one-shot run in a terminal or script. Reply in plain text or simple markdown; ```mermaid fences and GenUI show as source."
	case agent.EntryCron:
		line = "Interface: a scheduled run with no one watching live. The reply is read later in the Web UI, where markdown, ```mermaid fences and GenUI panels render."
	default:
		return ""
	}
	return "<system-reminder>\n" + line + "\n</system-reminder>"
}
