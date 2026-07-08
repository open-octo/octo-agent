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
	name      string
	stored    chan string
	recallOut []memorybackend.Result
	recallErr error
	storeErr  error
}

func (f *fakeMemoryBackend) Name() string { return f.name }

func (f *fakeMemoryBackend) Store(_ context.Context, content string) error {
	if f.stored != nil {
		f.stored <- content
	}
	return f.storeErr
}

func (f *fakeMemoryBackend) Recall(_ context.Context, _ string) ([]memorybackend.Result, error) {
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
