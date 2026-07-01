package hooks

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestEngine_NilSafe(t *testing.T) {
	var e *Engine
	if e.Configured(EventStop) {
		t.Error("nil engine should report not configured")
	}
	if got := e.Inject(context.Background(), Payload{Event: EventUserPromptSubmit}); got != "" {
		t.Errorf("nil engine Inject = %q; want empty", got)
	}
	e.Dispatch(context.Background(), Payload{Event: EventStop}) // must not panic
	if src, fire := e.SessionStartDecision("s", false, false); fire || src != "" {
		t.Errorf("nil engine SessionStartDecision = (%q,%v); want ('',false)", src, fire)
	}
}

func TestEngine_InjectInProcThenShellOrder(t *testing.T) {
	e := NewEngine(nil)
	e.RegisterInProc(EventUserPromptSubmit, func(_ context.Context, _ Payload) string { return "from-inproc" })
	e.RegisterShell(EventUserPromptSubmit, makeScript(t, "echo from-shell"), 0)

	got := e.Inject(context.Background(), Payload{Event: EventUserPromptSubmit, UserInput: "hi"})
	if got != "from-inproc\n\nfrom-shell" {
		t.Errorf("Inject order/join wrong: %q", got)
	}
}

func TestEngine_InjectShellStructuredEnvelope(t *testing.T) {
	e := NewEngine(nil)
	e.RegisterShell(EventUserPromptSubmit, makeScript(t, `echo '{"additional_context":"ctx"}'`), 0)
	if got := e.Inject(context.Background(), Payload{Event: EventUserPromptSubmit}); got != "ctx" {
		t.Errorf("structured envelope not parsed: %q", got)
	}
}

func TestEngine_InjectShellFailureIsNonBlocking(t *testing.T) {
	e := NewEngine(nil)
	var notices []string
	e.Notify = func(m string) { notices = append(notices, m) }
	e.RegisterInProc(EventUserPromptSubmit, func(_ context.Context, _ Payload) string { return "kept" })
	e.RegisterShell(EventUserPromptSubmit, makeScript(t, "echo boom >&2; exit 1"), 0)

	got := e.Inject(context.Background(), Payload{Event: EventUserPromptSubmit})
	if got != "kept" {
		t.Errorf("failing shell hook should not drop in-proc output; got %q", got)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "boom") {
		t.Errorf("shell failure should surface via Notify: %v", notices)
	}
}

func TestEngine_InjectIgnoresSideEffectEvent(t *testing.T) {
	e := NewEngine(nil)
	e.RegisterShell(EventStop, makeScript(t, "echo should-not-inject"), 0)
	if got := e.Inject(context.Background(), Payload{Event: EventStop}); got != "" {
		t.Errorf("Inject on a side-effect event must return empty; got %q", got)
	}
}

func TestEngine_DispatchRunsSideEffectAndIgnoresOutput(t *testing.T) {
	e := NewEngine(nil)
	dir := t.TempDir()
	marker := dir + "/ran"
	e.RegisterShell(EventStop, makeScript(t, "touch "+marker), 0)
	e.Dispatch(context.Background(), Payload{Event: EventStop, AssistantReply: "done"})
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("side-effect hook did not run: %v", err)
	}
}

func TestEngine_PostToolUsePayloadReachesStdin(t *testing.T) {
	e := NewEngine(nil)
	dir := t.TempDir()
	out := dir + "/captured.json"
	e.RegisterShell(EventPostToolUse, makeScript(t, "cat > "+out), 0)
	e.Inject(context.Background(), Payload{
		Event:     EventPostToolUse,
		ToolName:  "terminal",
		ToolInput: map[string]any{"command": "ls"},
	})
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"event":"PostToolUse"`, `"tool_name":"terminal"`, `"command":"ls"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("payload missing %q in:\n%s", want, b)
		}
	}
}

func TestEngine_SessionStartDecision(t *testing.T) {
	e := NewEngine(nil)

	// /clear always wins.
	if src, fire := e.SessionStartDecision("c", true, true); !fire || src != SourceClear {
		t.Errorf("clear = (%q,%v); want (clear,true)", src, fire)
	}

	// Never started → startup, once.
	if src, fire := e.SessionStartDecision("a", false, false); !fire || src != SourceStartup {
		t.Errorf("first = (%q,%v); want (startup,true)", src, fire)
	}
	// Same process, later turn → no fire even though it "started".
	if src, fire := e.SessionStartDecision("a", true, false); fire || src != "" {
		t.Errorf("seen-again = (%q,%v); want ('',false)", src, fire)
	}
}

func TestEngine_ResumeOncePerProcess(t *testing.T) {
	e := NewEngine(nil)
	// A previously-started session first touched by THIS process → resume.
	if src, fire := e.SessionStartDecision("b", true, false); !fire || src != SourceResume {
		t.Errorf("first-touch of started session = (%q,%v); want (resume,true)", src, fire)
	}
	// Subsequent turns in the same process → no fire.
	if src, fire := e.SessionStartDecision("b", true, false); fire || src != "" {
		t.Errorf("resume should fire once per process; got (%q,%v)", src, fire)
	}
}

func TestEngineFromEnv_MapsPreAndPost(t *testing.T) {
	t.Setenv("OCTO_HOOK_PRE_TURN", "/bin/pre")
	t.Setenv("OCTO_HOOK_POST_TURN", "/bin/post")
	t.Setenv("OCTO_HOOK_TIMEOUT", "")
	e := EngineFromEnv(nil)
	if !e.Configured(EventUserPromptSubmit) {
		t.Error("PRE_TURN should map to UserPromptSubmit")
	}
	if !e.Configured(EventStop) {
		t.Error("POST_TURN should map to Stop")
	}
	if e.Configured(EventPreToolUse) {
		t.Error("nothing should be registered for PreToolUse")
	}
}
