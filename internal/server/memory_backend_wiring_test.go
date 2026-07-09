package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/hooks"
	"github.com/open-octo/octo-agent/internal/tools"
)

// hindsightRecallStub is a minimal httptest double for the hindsight recall
// endpoint (see internal/memorybackend/hindsight.go's bankURL/Recall), used
// so tests can drive a REAL auto-recall hook invocation end to end (real
// HTTP round trip against localhost, no live network) and inspect its actual
// output — rather than re-querying the tools package-global state after the
// fact, which would pass even if the refresh under test ran too late (see
// this function's/sibling tests' doc comments for why that check is unsound).
func hindsightRecallStub(t *testing.T, marker string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{{"id": "m1", "text": marker, "scores": map[string]any{"final": 0.9}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestPrepareToolTurn_WiresMemoryBackend guards the serve path: unlike the CLI
// (app.WireTools), the server wires tools in prepareToolTurn, so it must build
// and register the configured memory backend itself — otherwise the feature
// silently doesn't exist under `octo serve` even when ~/.octo/config.yml
// configures one.
func TestPrepareToolTurn_WiresMemoryBackend(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Models:       []config.ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "gpt-4o",
		MemoryBackend: config.MemoryBackendConfig{
			Type:    "hindsight",
			BaseURL: "http://localhost:8888",
		},
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	tools.SetMemoryBackend(nil) // start disabled to prove prepareToolTurn is what enables it
	t.Cleanup(func() { tools.SetMemoryBackend(nil) })

	if _, _, _, _, err := srv.prepareToolTurn(context.Background(), agent.New(&stubSender{}, "gpt-4o"), nil); err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	if g := tools.MemoryBackendGuidance(); g == "" {
		t.Error("memory backend guidance is empty after prepareToolTurn; want it wired from config")
	}
}

func TestPrepareToolTurn_WiresAutoRecall(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Models:       []config.ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "gpt-4o",
		MemoryBackend: config.MemoryBackendConfig{
			Type:       "hindsight",
			BaseURL:    "http://localhost:8888",
			AutoRecall: true,
		},
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	tools.SetMemoryBackend(nil)
	tools.SetMemoryBackendAutoRecall(false)
	t.Cleanup(func() {
		tools.SetMemoryBackend(nil)
		tools.SetMemoryBackendAutoRecall(false)
	})

	if _, _, _, _, err := srv.prepareToolTurn(context.Background(), agent.New(&stubSender{}, "gpt-4o"), nil); err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}

	e := hooks.NewEngine(nil)
	tools.RegisterMemoryBackendHooks(e)
	if !e.Configured(hooks.EventUserPromptSubmit) {
		t.Error("prepareToolTurn should propagate auto_recall: true into an UserPromptSubmit hook")
	}
}

// TestBuildAgent_RefreshesAutoRecallBeforeRegisteringHooks guards against a
// real regression: buildAgent reads tools.MemoryBackendGuidance() and calls
// tools.RegisterMemoryBackendHooks BEFORE prepareToolTurn ever runs (in every
// caller — runTurn, the WS handler, the scheduled-task handler — buildAgent
// executes first in the same function). Relying on prepareToolTurn alone to
// keep the auto-recall flag fresh left a one-turn-stale window: a session
// hitting buildAgent right after auto_recall flips to true in config would
// still get built with the hook missing, because the global the previous
// turn's prepareToolTurn call last set was false. This deliberately leaves
// the global at false before calling buildAgent, with no prepareToolTurn call
// at all, to prove buildAgent's own refresh (not prepareToolTurn's) is what
// makes this work.
//
// Drives a REAL Inject() call against the returned agent's own a.Hooks and
// checks for actual recalled content — not e.Configured(EventUserPromptSubmit)
// on a.Hooks (the WorkflowNudger every session gets, unconditionally,
// registers its own no-op-output hook on that same event — "configured"
// would be true whether or not the auto-recall hook is there) and not a
// freshly-built throwaway engine re-queried after buildAgent returns (that
// only proves the global ends up correct eventually, not that THIS turn's
// engine was built with the right value at registration time — verified this
// is a real gap: temporarily moved buildAgent's refresh call to run after
// RegisterMemoryBackendHooks and both of those alternate assertions still
// passed).
func TestBuildAgent_RefreshesAutoRecallBeforeRegisteringHooks(t *testing.T) {
	setTestHome(t)
	marker := "octo-agent lucky number is 47"
	seedModels(t, config.Config{
		Models:       []config.ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "gpt-4o",
		MemoryBackend: config.MemoryBackendConfig{
			Type:       "hindsight",
			BaseURL:    hindsightRecallStub(t, marker),
			AutoRecall: true,
		},
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	tools.SetMemoryBackend(nil)
	tools.SetMemoryBackendAutoRecall(false) // simulate the previous turn's stale state
	t.Cleanup(func() {
		tools.SetMemoryBackend(nil)
		tools.SetMemoryBackendAutoRecall(false)
	})

	sess := agent.NewSession("gpt-4o", "")
	a := srv.buildAgent(sess)

	if a.Hooks == nil {
		t.Fatal("buildAgent should set a.Hooks")
	}
	got := a.Hooks.Inject(context.Background(), hooks.Payload{
		Event:     hooks.EventUserPromptSubmit,
		UserInput: "what's my lucky number?",
	})
	if !strings.Contains(got, marker) {
		t.Errorf("injected UserPromptSubmit text = %q, want it to contain the recalled marker %q — buildAgent must refresh the memory backend before registering hooks, not after", got, marker)
	}
}

func TestPrepareToolTurn_NoMemoryBackendConfigured(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Models:       []config.ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "gpt-4o",
	})
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	tools.SetMemoryBackend(nil)
	t.Cleanup(func() { tools.SetMemoryBackend(nil) })

	if _, _, _, _, err := srv.prepareToolTurn(context.Background(), agent.New(&stubSender{}, "gpt-4o"), nil); err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	if g := tools.MemoryBackendGuidance(); g != "" {
		t.Errorf("memory backend guidance = %q, want empty with no memory_backend configured", g)
	}
}
