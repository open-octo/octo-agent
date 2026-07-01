package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/hooks"
)

// sessionStartAgent builds an agent with an isolated hook engine carrying a
// single in-process SessionStart hook that records the source it saw and
// returns injected text.
func sessionStartAgent(t *testing.T, id, inject string, sawSource *string) *Agent {
	t.Helper()
	send := &fakeSender{reply: Reply{Content: "ok"}}
	a := New(send, "m")
	a.HookMeta = hooks.Meta{SessionID: id}
	a.Hooks = hooks.NewEngine(nil) // private seen-set → test isolation
	a.Hooks.RegisterInProc(hooks.EventSessionStart, func(_ context.Context, p hooks.Payload) string {
		*sawSource = p.Source
		return inject
	})
	return a
}

func TestSessionStart_StartupFiresPersistsAndFolds(t *testing.T) {
	var src string
	a := sessionStartAgent(t, "s-startup", "[warmup]", &src)
	var persisted bool
	a.OnSessionStart = func() { persisted = true }

	if _, err := a.Turn(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if src != hooks.SourceStartup {
		t.Errorf("source = %q, want startup", src)
	}
	if !persisted {
		t.Error("OnSessionStart must fire on startup so the layer persists the flag")
	}
	if !a.SessionStarted {
		t.Error("SessionStarted must be set after startup")
	}
	send := a.Sender.(*fakeSender)
	got := userText(send.gotMessages[0])
	if !strings.Contains(got, "[warmup]") || !strings.HasSuffix(got, "hello") {
		t.Errorf("SessionStart output not folded into the first user message: %q", got)
	}
}

func TestSessionStart_ResumeWhenAlreadyStarted(t *testing.T) {
	var src string
	a := sessionStartAgent(t, "s-resume", "", &src)
	a.SessionStarted = true // durable flag: started in a prior process

	if _, err := a.Turn(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if src != hooks.SourceResume {
		t.Errorf("source = %q, want resume (started before, first touch this process)", src)
	}
}

func TestSessionStart_FiresOncePerAgent(t *testing.T) {
	var src string
	fires := 0
	send := &fakeSender{reply: Reply{Content: "ok"}}
	a := New(send, "m")
	a.HookMeta = hooks.Meta{SessionID: "s-once"}
	a.Hooks = hooks.NewEngine(nil)
	a.Hooks.RegisterInProc(hooks.EventSessionStart, func(_ context.Context, p hooks.Payload) string {
		fires++
		src = p.Source
		return ""
	})

	for i := 0; i < 3; i++ {
		if _, err := a.Turn(context.Background(), "msg"); err != nil {
			t.Fatal(err)
		}
	}
	if fires != 1 {
		t.Errorf("SessionStart fired %d times across 3 turns, want 1 (startup then quiet)", fires)
	}
	if src != hooks.SourceStartup {
		t.Errorf("first fire source = %q, want startup", src)
	}
}

func TestSessionStart_ClearReopensAsClear(t *testing.T) {
	var src string
	a := sessionStartAgent(t, "s-clear", "", &src)
	a.SessionStarted = true // already open

	a.ClearHistory() // arms HookClear
	if _, err := a.Turn(context.Background(), "again"); err != nil {
		t.Fatal(err)
	}
	if src != hooks.SourceClear {
		t.Errorf("source after /clear = %q, want clear", src)
	}
	if a.HookClear {
		t.Error("HookClear must be consumed after firing")
	}
}
