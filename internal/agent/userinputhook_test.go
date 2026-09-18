package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/hooks"
)

// userText returns the plain text of a user message regardless of whether it's
// stored as Content or a single text block.
func userText(m Message) string {
	if m.Content != "" {
		return m.Content
	}
	for _, b := range m.Blocks {
		if b.Text != "" {
			return b.Text
		}
	}
	return ""
}

// promptSubmitEngine returns an engine with a single in-process UserPromptSubmit
// hook, the shape the memory injector's reminder takes after the redesign.
func promptSubmitEngine(fn func(userInput string) string) *hooks.Engine {
	e := hooks.NewEngine(nil)
	e.RegisterInProc(hooks.EventUserPromptSubmit, func(_ context.Context, p hooks.Payload) string {
		return fn(p.UserInput)
	})
	return e
}

func TestUserPromptSubmit_PrependsInjectionAsSingleMessage(t *testing.T) {
	send := &fakeSender{reply: Reply{Content: "ok"}}
	a := New(send, "m")
	a.Hooks = promptSubmitEngine(func(in string) string { return "<reminder>" + in + "</reminder>" })

	if _, err := a.Turn(context.Background(), "deploy please"); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	if len(send.gotMessages) != 1 {
		t.Fatalf("sender saw %d messages, want 1 (injection must fold into the user turn)", len(send.gotMessages))
	}
	got := userText(send.gotMessages[0])
	if !strings.Contains(got, "<reminder>deploy please</reminder>") {
		t.Errorf("injection not prepended: %q", got)
	}
	if !strings.HasSuffix(got, "deploy please") {
		t.Errorf("original input not preserved at the end: %q", got)
	}
}

// A shell hook's stdout rides the persisted user turn, so the display
// surfaces must be able to strip it back to exactly what the user typed —
// otherwise an external retrieval hook's notes render inside the user's own
// bubble. This pins the cross-package contract between hooks (which frames
// the output) and StripSystemReminders (which hides the frame).
func TestUserPromptSubmit_ShellOutputStripsBackToUserInput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook scripts use sh -c; not portable to Windows")
	}
	script := filepath.Join(t.TempDir(), "hook.sh")
	body := "#!/bin/sh\necho 'recalled note one'\necho 'recalled note two'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	e := hooks.NewEngine(nil)
	e.RegisterShell(hooks.EventUserPromptSubmit, script, 0)

	send := &fakeSender{reply: Reply{Content: "ok"}}
	a := New(send, "m")
	a.Hooks = e
	if _, err := a.Turn(context.Background(), "print today's weather"); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	msgs := a.History.Snapshot()
	if len(msgs) == 0 || msgs[0].Role != RoleUser {
		t.Fatalf("history[0] should be the user turn, got %+v", msgs)
	}
	persisted := userText(msgs[0])
	if !strings.Contains(persisted, "recalled note two") {
		t.Fatalf("hook output must still reach the model: %q", persisted)
	}
	if got := strings.TrimSpace(StripSystemReminders(persisted)); got != "print today's weather" {
		t.Errorf("display text after stripping = %q, want exactly the user's words", got)
	}
}

func TestUserPromptSubmit_EmptyReturnLeavesInputUntouched(t *testing.T) {
	send := &fakeSender{reply: Reply{Content: "ok"}}
	a := New(send, "m")
	a.Hooks = promptSubmitEngine(func(string) string { return "" })

	if _, err := a.Turn(context.Background(), "hello"); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if got := userText(send.gotMessages[0]); got != "hello" {
		t.Errorf("input should be untouched, got %q", got)
	}
}

func TestUserPromptSubmit_ErrorPathPopsCombinedMessage(t *testing.T) {
	send := &fakeSender{err: errors.New("boom")}
	a := New(send, "m")
	a.Hooks = promptSubmitEngine(func(in string) string { return "REMINDER\n\n" + in })

	if _, err := a.Turn(context.Background(), "hi"); err == nil {
		t.Fatal("expected error")
	}
	if n := a.History.Len(); n != 0 {
		t.Errorf("History.Len after failed Turn = %d, want 0 (the combined user message must be rolled back)", n)
	}
}
