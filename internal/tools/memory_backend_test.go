package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/hooks"
	"github.com/open-octo/octo-agent/internal/memorybackend"
)

// fakeMemoryBackend is a test double: Store reports each call on a buffered
// channel (so tests can synchronize on the async hook without sleeping),
// Recall returns canned results or an error.
type fakeMemoryBackend struct {
	name        string
	stored      chan string
	recallOut   []memorybackend.Result
	recallErr   error
	storeErr    error
	recallQuery string
}

func (f *fakeMemoryBackend) Name() string { return f.name }

func (f *fakeMemoryBackend) Store(_ context.Context, content string) error {
	if f.stored != nil {
		f.stored <- content
	}
	return f.storeErr
}

func (f *fakeMemoryBackend) Recall(_ context.Context, query string) ([]memorybackend.Result, error) {
	f.recallQuery = query
	return f.recallOut, f.recallErr
}

func setMemoryBackendFor(t *testing.T, b memorybackend.Backend) {
	t.Helper()
	SetMemoryBackend(b)
	t.Cleanup(func() { SetMemoryBackend(nil) })
}

func TestMemoryRecallTool_Disabled(t *testing.T) {
	SetMemoryBackend(nil)
	t.Cleanup(func() { SetMemoryBackend(nil) })

	if _, err := (MemoryRecallTool{}).Execute(context.Background(), "memory_recall", map[string]any{"query": "x"}); err == nil {
		t.Error("expected error when no backend is configured")
	}
}

func TestMemoryRecallTool_MissingQuery(t *testing.T) {
	setMemoryBackendFor(t, &fakeMemoryBackend{name: "hindsight"})
	if _, err := (MemoryRecallTool{}).Execute(context.Background(), "memory_recall", map[string]any{}); err == nil {
		t.Error("expected error for missing query")
	}
}

func TestMemoryRecallTool_NoResults(t *testing.T) {
	setMemoryBackendFor(t, &fakeMemoryBackend{name: "hindsight"})
	out, err := (MemoryRecallTool{}).Execute(context.Background(), "memory_recall", map[string]any{"query": "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.Text, "No relevant memories") {
		t.Errorf("text = %q, want a no-results message", out.Text)
	}
}

func TestMemoryRecallTool_FormatsResults(t *testing.T) {
	setMemoryBackendFor(t, &fakeMemoryBackend{
		name: "hindsight",
		recallOut: []memorybackend.Result{
			{ID: "m1", Content: "likes tabs", Score: 0.9},
			{ID: "m2", Content: "prefers Go"},
		},
	})
	out, err := (MemoryRecallTool{}).Execute(context.Background(), "memory_recall", map[string]any{"query": "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.Text, "likes tabs") || !strings.Contains(out.Text, "0.90") {
		t.Errorf("text missing scored result: %q", out.Text)
	}
	if !strings.Contains(out.Text, "prefers Go") {
		t.Errorf("text missing unscored result: %q", out.Text)
	}
}

func TestMemoryRecallTool_BackendError(t *testing.T) {
	setMemoryBackendFor(t, &fakeMemoryBackend{name: "hindsight", recallErr: errors.New("boom")})
	if _, err := (MemoryRecallTool{}).Execute(context.Background(), "memory_recall", map[string]any{"query": "x"}); err == nil {
		t.Error("expected error to propagate from backend")
	}
}

func TestMemoryBackendGuidance(t *testing.T) {
	SetMemoryBackend(nil)
	if g := MemoryBackendGuidance(); g != "" {
		t.Errorf("disabled: guidance = %q, want empty", g)
	}

	setMemoryBackendFor(t, &fakeMemoryBackend{name: "mem0"})
	g := MemoryBackendGuidance()
	if !strings.Contains(g, "mem0") || !strings.Contains(g, "memory_recall") {
		t.Errorf("enabled: guidance = %q, want it to name the backend and memory_recall", g)
	}
}

func TestRegisterMemoryBackendHooks_NoopWhenDisabled(t *testing.T) {
	SetMemoryBackend(nil)
	e := hooks.NewEngine(nil)
	RegisterMemoryBackendHooks(e)
	if e.Configured(hooks.EventStop) {
		t.Error("expected no Stop hook registered when no backend is configured")
	}
}

func TestRegisterMemoryBackendHooks_StoresOnStop(t *testing.T) {
	fake := &fakeMemoryBackend{name: "hindsight", stored: make(chan string, 1)}
	setMemoryBackendFor(t, fake)

	e := hooks.NewEngine(nil)
	RegisterMemoryBackendHooks(e)
	e.Dispatch(context.Background(), hooks.Payload{
		Event:          hooks.EventStop,
		UserInput:      "what's my editor indentation preference?",
		AssistantReply: "tabs",
	})

	select {
	case got := <-fake.stored:
		if !strings.Contains(got, "what's my editor indentation preference?") || !strings.Contains(got, "tabs") {
			t.Errorf("stored content = %q, want it to contain both turn halves", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Store was not called within timeout")
	}
}

func TestRegisterMemoryBackendHooks_SkipsEmptyTurn(t *testing.T) {
	fake := &fakeMemoryBackend{name: "hindsight", stored: make(chan string, 1)}
	setMemoryBackendFor(t, fake)

	e := hooks.NewEngine(nil)
	RegisterMemoryBackendHooks(e)
	e.Dispatch(context.Background(), hooks.Payload{Event: hooks.EventStop})

	select {
	case got := <-fake.stored:
		t.Fatalf("Store called for an empty turn: %q", got)
	case <-time.After(200 * time.Millisecond):
		// expected: nothing stored
	}
}

func TestRegisterMemoryBackendHooks_AutoRecallDisabledByDefault(t *testing.T) {
	setMemoryBackendFor(t, &fakeMemoryBackend{name: "hindsight"})
	t.Cleanup(func() { SetMemoryBackendAutoRecall(false) })

	e := hooks.NewEngine(nil)
	RegisterMemoryBackendHooks(e)
	if e.Configured(hooks.EventUserPromptSubmit) {
		t.Error("expected no UserPromptSubmit hook when auto_recall is off")
	}
}

func TestRegisterMemoryBackendHooks_AutoRecallInjectsContext(t *testing.T) {
	fake := &fakeMemoryBackend{
		name:      "hindsight",
		recallOut: []memorybackend.Result{{ID: "m1", Content: "lucky number is 47"}},
	}
	setMemoryBackendFor(t, fake)
	SetMemoryBackendAutoRecall(true)
	t.Cleanup(func() { SetMemoryBackendAutoRecall(false) })

	e := hooks.NewEngine(nil)
	RegisterMemoryBackendHooks(e)
	got := e.Inject(context.Background(), hooks.Payload{
		Event:     hooks.EventUserPromptSubmit,
		UserInput: "what's my lucky number?",
	})

	if fake.recallQuery != "what's my lucky number?" {
		t.Errorf("Recall query = %q, want the user's message", fake.recallQuery)
	}
	if !strings.Contains(got, "lucky number is 47") {
		t.Errorf("injected text = %q, want it to contain the recalled memory", got)
	}
	if !strings.Contains(got, "no need to call") || !strings.Contains(got, "memory_recall") {
		t.Errorf("injected text = %q, want it to discourage a redundant memory_recall call", got)
	}
}

func TestRegisterMemoryBackendHooks_AutoRecallSwallowsErrorsAndEmptyResults(t *testing.T) {
	fake := &fakeMemoryBackend{name: "hindsight", recallErr: errors.New("boom")}
	setMemoryBackendFor(t, fake)
	SetMemoryBackendAutoRecall(true)
	t.Cleanup(func() { SetMemoryBackendAutoRecall(false) })

	e := hooks.NewEngine(nil)
	RegisterMemoryBackendHooks(e)
	if got := e.Inject(context.Background(), hooks.Payload{Event: hooks.EventUserPromptSubmit, UserInput: "x"}); got != "" {
		t.Errorf("expected empty injection on Recall error, got %q", got)
	}

	fake.recallErr = nil
	fake.recallOut = nil
	if got := e.Inject(context.Background(), hooks.Payload{Event: hooks.EventUserPromptSubmit, UserInput: "x"}); got != "" {
		t.Errorf("expected empty injection on empty Recall results, got %q", got)
	}
}

func TestRegisterMemoryBackendHooks_AutoRecallSkipsEmptyInput(t *testing.T) {
	fake := &fakeMemoryBackend{name: "hindsight", recallOut: []memorybackend.Result{{Content: "should not appear"}}}
	setMemoryBackendFor(t, fake)
	SetMemoryBackendAutoRecall(true)
	t.Cleanup(func() { SetMemoryBackendAutoRecall(false) })

	e := hooks.NewEngine(nil)
	RegisterMemoryBackendHooks(e)
	if got := e.Inject(context.Background(), hooks.Payload{Event: hooks.EventUserPromptSubmit}); got != "" {
		t.Errorf("expected empty injection for empty UserInput, got %q", got)
	}
}

// TestRegisterMemoryBackendHooks_StripsSystemRemindersBeforeStore guards against
// <system-reminder> spans (recalled memories, goal context, background-task
// notes) landing in long-term memory — they'd resurface via memory_recall and
// ride the tool_result path to the web UI.
func TestRegisterMemoryBackendHooks_StripsSystemRemindersBeforeStore(t *testing.T) {
	fake := &fakeMemoryBackend{name: "hindsight", stored: make(chan string, 1)}
	setMemoryBackendFor(t, fake)

	e := hooks.NewEngine(nil)
	RegisterMemoryBackendHooks(e)
	e.Dispatch(context.Background(), hooks.Payload{
		Event:          hooks.EventStop,
		UserInput:      "<system-reminder>\n[OLD MEMORY] legacy content\n</system-reminder>Hello",
		AssistantReply: "Hi",
	})

	select {
	case got := <-fake.stored:
		if strings.Contains(got, "<system-reminder>") || strings.Contains(got, "</system-reminder>") {
			t.Errorf("stored content should have system-reminder spans stripped, got %q", got)
		}
		if strings.Contains(got, "legacy content") {
			t.Errorf("stored content should not contain reminder body text, got %q", got)
		}
		if !strings.Contains(got, "Hello") || !strings.Contains(got, "Hi") {
			t.Errorf("stored content should keep real turn text, got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Store was not called within timeout")
	}
}

// TestRegisterMemoryBackendHooks_AutoRecallStripsSystemReminders guards Recall
// results that carry pre-existing <system-reminder> spans (e.g. from before
// this fix, or external index data) — they must be stripped before the
// automatic user-prompt injection, not re-injected verbatim.
func TestRegisterMemoryBackendHooks_AutoRecallStripsSystemReminders(t *testing.T) {
	fake := &fakeMemoryBackend{
		name: "hindsight",
		recallOut: []memorybackend.Result{
			{ID: "m1", Content: "<system-reminder>\nStale recall body\n</system-reminder>real fact"},
		},
	}
	setMemoryBackendFor(t, fake)
	SetMemoryBackendAutoRecall(true)
	t.Cleanup(func() { SetMemoryBackendAutoRecall(false) })

	e := hooks.NewEngine(nil)
	RegisterMemoryBackendHooks(e)
	got := e.Inject(context.Background(), hooks.Payload{
		Event:     hooks.EventUserPromptSubmit,
		UserInput: "question",
	})

	// The hook intentionally wraps recalled memory in <system-reminder>, so we
	// expect exactly one open tag — two would mean the nested span leaked
	// through. The nested reminder body must be gone.
	if gotCount := strings.Count(got, "<system-reminder>"); gotCount != 1 {
		t.Errorf("injected text should have exactly one <system-reminder> wrapper, found %d in %q", gotCount, got)
	}
	if strings.Contains(got, "Stale recall body") {
		t.Errorf("injected text should not contain reminder body from recall result, got %q", got)
	}
	if !strings.Contains(got, "real fact") {
		t.Errorf("injected text should keep the real part of the recall, got %q", got)
	}
}
