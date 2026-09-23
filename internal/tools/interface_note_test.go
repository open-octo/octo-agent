package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/hooks"
)

func TestInterfaceReminder_PerEntry(t *testing.T) {
	cases := []struct {
		transport string
		want      string // substring; "" means no reminder at all
	}{
		{agent.EntryWeb, "Interface: the Web UI."},
		{agent.EntryChannel, "Interface: an IM chat."},
		{agent.EntryTUI, "Interface: the terminal UI."},
		{agent.EntryCLI, "Interface: a one-shot run"},
		{agent.EntryCron, "Interface: a scheduled run"},
		{agent.EntryAPI, ""},
		{"", ""},
	}
	for _, c := range cases {
		got := InterfaceReminder(c.transport)
		if c.want == "" {
			if got != "" {
				t.Errorf("%q: got %q, want no reminder", c.transport, got)
			}
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%q: got %q, want it to contain %q", c.transport, got, c.want)
		}
		// The frame is what hides it in every transcript view.
		if !strings.HasPrefix(got, "<system-reminder>\n") || !strings.HasSuffix(got, "\n</system-reminder>") {
			t.Errorf("%q: not framed as a system reminder: %q", c.transport, got)
		}
	}
}

// Drives the real hook engine so a wiring mistake (wrong event, wrong payload
// field) is caught, and checks the note follows the payload's transport turn
// by turn — the same engine serves turns from different entries.
func TestInterfaceNote_HookPath(t *testing.T) {
	e := hooks.NewEngine(nil)
	NewInterfaceNote().RegisterHooks(e)
	ctx := context.Background()
	submit := func(transport string) string {
		return e.Inject(ctx, hooks.Payload{Event: hooks.EventUserPromptSubmit, Transport: transport, UserInput: "hi"})
	}

	if got := submit(agent.EntryWeb); !strings.Contains(got, "Web UI") {
		t.Fatalf("web turn: got %q", got)
	}
	if got := submit(agent.EntryChannel); !strings.Contains(got, "IM chat") {
		t.Fatalf("channel turn after a web turn: got %q", got)
	}
	if got := e.Inject(ctx, hooks.Payload{Event: hooks.EventPostToolUse, Transport: agent.EntryWeb, ToolName: "read_file"}); strings.Contains(got, "Interface:") {
		t.Fatalf("note must only ride the user turn, got %q on PostToolUse", got)
	}
}
