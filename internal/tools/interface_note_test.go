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
		{agent.EntryAPI, "Interface: an API client"},
		{agent.EntrySetup, ""},
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
// field) is caught. With no agent the note has no history to consult and is
// sent every turn, following the payload's transport.
func TestInterfaceNote_HookPath(t *testing.T) {
	e := hooks.NewEngine(nil)
	NewInterfaceNote(nil).RegisterHooks(e)
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

// The note is sent only when the latest one in the history the model will see
// names a different interface, or there is none.
func TestInterfaceNote_OnlyWhenChanged(t *testing.T) {
	web, im := InterfaceReminder(agent.EntryWeb), InterfaceReminder(agent.EntryChannel)
	user := func(prefix, text string) agent.Message {
		if prefix == "" {
			return agent.NewUserMessage(text)
		}
		return agent.NewUserMessage(prefix + "\n\n" + text)
	}
	cases := []struct {
		name      string
		history   []agent.Message
		transport string
		wantNote  bool
	}{
		{"empty history", nil, agent.EntryWeb, true},
		{"same interface as the latest note", []agent.Message{user(web, "a"), agent.NewAssistantMessage("b"), user("", "c")}, agent.EntryWeb, false},
		{"switched from IM", []agent.Message{user(web, "a"), user(im, "b")}, agent.EntryWeb, true},
		{"switched back: only the latest note counts", []agent.Message{user(im, "a"), user(web, "b")}, agent.EntryWeb, false},
		{"note compacted away", []agent.Message{user("", "[Earlier conversation summary]\n\n..."), agent.NewAssistantMessage("ok")}, agent.EntryWeb, true},
		{"note in a text block beside an image", []agent.Message{{Role: agent.RoleUser, Blocks: []agent.ContentBlock{agent.NewTextBlock(web + "\n\nlook")}}}, agent.EntryWeb, false},
		{"user quoting the words is not a note", []agent.Message{user("", "Interface: the Web UI.")}, agent.EntryWeb, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := agent.New(nil, "m")
			a.History.ReplaceAll(c.history)
			e := hooks.NewEngine(nil)
			NewInterfaceNote(a).RegisterHooks(e)
			got := e.Inject(context.Background(), hooks.Payload{Event: hooks.EventUserPromptSubmit, Transport: c.transport, UserInput: "next"})
			if c.wantNote && !strings.Contains(got, "Interface:") {
				t.Errorf("want a note, got %q", got)
			}
			if !c.wantNote && got != "" {
				t.Errorf("want no note, got %q", got)
			}
		})
	}
}

// A scheduled task with notify targets pushes its reply to IM, so the cron
// note must not promise Web-only rendering there.
func TestInterfaceNote_CronReplySentToIM(t *testing.T) {
	e := hooks.NewEngine(nil)
	NewInterfaceNote(nil).RegisterHooks(e)
	p := hooks.Payload{Event: hooks.EventUserPromptSubmit, Transport: agent.EntryCron, UserInput: "run"}

	if got := e.Inject(context.Background(), p); !strings.Contains(got, "read later in the Web UI") {
		t.Errorf("plain cron run: got %q", got)
	}
	got := e.Inject(WithReplyToIM(context.Background()), p)
	if !strings.Contains(got, "also sent to an IM chat") || strings.Contains(got, "GenUI panels render") {
		t.Errorf("cron run with IM notify: got %q", got)
	}
	if !strings.HasPrefix(got, interfaceNotePrefix) {
		t.Errorf("cron-to-IM note must be recognisable as a note: %q", got)
	}
}
