package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
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

func TestUserInputHook_PrependsReminderAsSingleMessage(t *testing.T) {
	send := &fakeSender{reply: Reply{Content: "ok"}}
	a := New(send, "m")
	a.UserInputHook = func(in string) string { return "<reminder>" + in + "</reminder>" }

	if _, err := a.Turn(context.Background(), "deploy please"); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	if len(send.gotMessages) != 1 {
		t.Fatalf("sender saw %d messages, want 1 (reminder must fold into the user turn)", len(send.gotMessages))
	}
	got := userText(send.gotMessages[0])
	if !strings.Contains(got, "<reminder>deploy please</reminder>") {
		t.Errorf("reminder not prepended: %q", got)
	}
	if !strings.HasSuffix(got, "deploy please") {
		t.Errorf("original input not preserved at the end: %q", got)
	}
}

func TestUserInputHook_EmptyReturnLeavesInputUntouched(t *testing.T) {
	send := &fakeSender{reply: Reply{Content: "ok"}}
	a := New(send, "m")
	a.UserInputHook = func(string) string { return "" }

	if _, err := a.Turn(context.Background(), "hello"); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if got := userText(send.gotMessages[0]); got != "hello" {
		t.Errorf("input should be untouched, got %q", got)
	}
}

func TestUserInputHook_ErrorPathPopsCombinedMessage(t *testing.T) {
	send := &fakeSender{err: errors.New("boom")}
	a := New(send, "m")
	a.UserInputHook = func(in string) string { return "REMINDER\n\n" + in }

	if _, err := a.Turn(context.Background(), "hi"); err == nil {
		t.Fatal("expected error")
	}
	if n := a.History.Len(); n != 0 {
		t.Errorf("History.Len after failed Turn = %d, want 0 (the combined user message must be rolled back)", n)
	}
}
