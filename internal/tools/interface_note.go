package tools

import (
	"context"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/hooks"
)

// InterfaceNote tells the model which interface its reply will be read in.
// What renders differs sharply — the Web UI draws ```mermaid fences and GenUI
// panels, an IM chat and the terminal show them as source — and nothing else
// in the context says where the user is.
//
// It rides the user message rather than the system prompt for two reasons.
// A session can change interface mid-conversation (the Web UI can take over
// a session an IM chat was driving, and the reverse), which a frozen prompt
// could not follow; and rewriting the prompt to follow it would break the
// provider's cached prefix. Prepending to the new user message leaves every
// earlier byte untouched.
//
// It is sent only when the interface differs from the latest note in the
// history the model is about to see: at the start, after a switch, and after
// compaction summarized the last note away. That history — not a flag kept on
// this value — is the judge, because what the model sees is exactly the
// agent's history, whichever process last wrote it.
type InterfaceNote struct {
	agent *agent.Agent
}

// NewInterfaceNote returns a note that reads a's history when it fires. a may
// be nil; the note is then sent every turn.
func NewInterfaceNote(a *agent.Agent) *InterfaceNote { return &InterfaceNote{agent: a} }

// RegisterHooks wires the note onto a session's hook engine for
// UserPromptSubmit. The interface comes from the payload's Transport, which
// every turn-starting call site sets to the entry actually running the turn.
func (n *InterfaceNote) RegisterHooks(e *hooks.Engine) {
	if n == nil || e == nil {
		return
	}
	e.RegisterInProc(hooks.EventUserPromptSubmit, func(_ context.Context, p hooks.Payload) string {
		r := InterfaceReminder(p.Transport)
		if r == "" || n.lastNote() == r {
			return ""
		}
		return r
	})
}

const interfaceNotePrefix = "<system-reminder>\nInterface: "

// lastNote returns the most recent interface note in the agent's history, or
// "" when there is none. It reads the history at call time: the agent's
// history is assigned after the hooks are wired, and an IM turn replaces it.
func (n *InterfaceNote) lastNote() string {
	if n.agent == nil || n.agent.History == nil {
		return ""
	}
	msgs := n.agent.History.Snapshot()
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != agent.RoleUser {
			continue
		}
		texts := []string{m.Content}
		for _, b := range m.Blocks {
			if b.Type == "text" {
				texts = append(texts, b.Text)
			}
		}
		for j := len(texts) - 1; j >= 0; j-- {
			if note := noteIn(texts[j]); note != "" {
				return note
			}
		}
	}
	return ""
}

// noteIn extracts the last interface note in s, frame included.
func noteIn(s string) string {
	start := strings.LastIndex(s, interfaceNotePrefix)
	if start < 0 {
		return ""
	}
	const closing = "\n</system-reminder>"
	end := strings.Index(s[start:], closing)
	if end < 0 {
		return ""
	}
	return s[start : start+end+len(closing)]
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
