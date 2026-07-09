package server

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/tools"
)

// TestEnableSubAgentToolsAdvertises verifies that starting a tools-enabled
// server registers the gating sentinels so DefaultToolsFor advertises the
// sub-agent and task tools (which would otherwise be withheld).
func TestEnableSubAgentToolsAdvertises(t *testing.T) {
	srv := &Server{
		cfg:       Config{Tools: true},
		sender:    &stubSender{},
		model:     "stub-model",
		cwd:       t.TempDir(),
		turnLocks: map[string]*sync.Mutex{},
	}
	srv.enableSubAgentTools()
	t.Cleanup(func() {
		tools.SetDefaultSubAgentManager(nil)
		tools.SetTaskStore(nil)
	})

	names := map[string]bool{}
	for _, d := range tools.DefaultToolsFor("") {
		names[d.Name] = true
	}
	for _, want := range []string{"sub_agent", "task_create", "task_list"} {
		if !names[want] {
			t.Errorf("expected %q to be advertised after enableSubAgentTools", want)
		}
	}
}

// systemRecordingSender is scriptedSender plus capturing the `system` prompt
// each call received, indexed by call order. Needed because
// TestEnableSubAgentTools_RefreshesMemoryBackendBeforeBakingGuidance must
// verify what actually got baked into the sub-agent template's System — not
// re-read tools.MemoryBackendGuidance() after the fact, which would pass even
// if enableSubAgentTools refreshed the backend too late (after baking), since
// that global would still end up correct by the time the function returns.
type systemRecordingSender struct {
	mu      sync.Mutex
	replies []agent.Reply
	systems []string
}

func (s *systemRecordingSender) next(system string) agent.Reply {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.systems = append(s.systems, system)
	var r agent.Reply
	if len(s.systems)-1 < len(s.replies) {
		r = s.replies[len(s.systems)-1]
	} else {
		r = agent.Reply{Content: "fallback"}
	}
	return r
}

func (s *systemRecordingSender) systemAt(i int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < 0 || i >= len(s.systems) {
		return ""
	}
	return s.systems[i]
}

func (s *systemRecordingSender) SendMessages(_ context.Context, _, system string, _ []agent.Message, _ int) (agent.Reply, error) {
	return s.next(system), nil
}

func (s *systemRecordingSender) StreamMessages(_ context.Context, _, system string, _ []agent.Message, _ int, onChunk func(string), _ func(string)) (agent.Reply, error) {
	r := s.next(system)
	if onChunk != nil && r.Content != "" {
		onChunk(r.Content)
	}
	return r, nil
}

func (s *systemRecordingSender) SendMessagesWithTools(_ context.Context, _, system string, _ []agent.Message, _ int, _ []agent.ToolDefinition) (agent.Reply, error) {
	return s.next(system), nil
}

func (s *systemRecordingSender) StreamMessagesWithTools(_ context.Context, _, system string, _ []agent.Message, _ int, _ []agent.ToolDefinition, onChunk func(string), _ agent.ToolInputDeltaFunc, _ agent.ThinkingDeltaFunc) (agent.Reply, error) {
	r := s.next(system)
	if onChunk != nil && r.Content != "" {
		onChunk(r.Content)
	}
	return r, nil
}

// TestEnableSubAgentTools_RefreshesMemoryBackendBeforeBakingGuidance guards a
// related staleness bug found while fixing #1274: enableSubAgentTools bakes
// tools.MemoryBackendGuidance() into the sub-agent template's System prompt
// ONCE — at server startup, and again after onboarding — and spawned
// sub-agents reuse that baked template rather than recomposing per spawn
// (unlike buildAgent/runChannelTurns, which do recompose every turn). Without
// refreshing the memory-backend globals first, a server that starts with
// memory_backend already configured would still bake an empty guidance
// block, because nothing had ever called the refresh before this function's
// first-ever invocation. Simulates that cold-start ordering directly: sets
// the backend to nil (as it is before any turn or startup hook has touched
// it) before calling enableSubAgentTools.
//
// Drives the spawn via tools.DefaultSubAgentManager().RunSync directly
// (rather than srv.runTurn) with a plain context.Background(): a runTurn-driven
// spawn goes through prepareToolTurn's CTX-SCOPED sub-agent manager (built
// fresh per turn from buildAgent's own, already-correct agent — see #1133 /
// resolveSubAgentManager), which takes priority over and completely bypasses
// enableSubAgentTools's process-global manager, so it can't observe this
// function's bug at all. Also inspects the system prompt the sub-agent's own
// LLM call actually received, rather than re-querying
// tools.MemoryBackendGuidance() after enableSubAgentTools returns (which
// would pass even if the refresh ran too late to affect the baked template —
// see this function's sibling tests in memory_backend_wiring_test.go and
// channel_route_test.go for the same fix applied to buildAgent/runChannelTurns).
func TestEnableSubAgentTools_RefreshesMemoryBackendBeforeBakingGuidance(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{
		Models:       []config.ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "gpt-4o",
		MemoryBackend: config.MemoryBackendConfig{
			Type:    "hindsight",
			BaseURL: "http://localhost:8888",
		},
	})

	// The sub-agent's own reply (no tools) ends its loop after one call — the
	// `system` param that call received (captured by systemRecordingSender)
	// is what this test inspects.
	sender := &systemRecordingSender{replies: []agent.Reply{{Content: "child result"}}}

	srv := &Server{
		cfg:       Config{Tools: true},
		sender:    sender,
		model:     "stub-model",
		cwd:       t.TempDir(),
		turnLocks: map[string]*sync.Mutex{},
	}

	tools.SetMemoryBackend(nil) // simulate cold start: nothing has refreshed this yet
	t.Cleanup(func() {
		tools.SetDefaultSubAgentManager(nil)
		tools.SetTaskStore(nil)
		tools.SetMemoryBackend(nil)
	})

	srv.enableSubAgentTools()

	mgr := tools.DefaultSubAgentManager()
	if mgr == nil {
		t.Fatal("enableSubAgentTools should have registered a process-global SubAgentManager")
	}
	if _, err := mgr.RunSync(context.Background(), tools.SpawnRequest{Description: "d", Prompt: "do the sub task"}); err != nil {
		t.Fatalf("RunSync: %v", err)
	}

	childSystem := sender.systemAt(0)
	if !strings.Contains(childSystem, "Memory backend") {
		t.Errorf("sub-agent's actual system prompt = %q, want it to contain the memory-backend guidance baked by enableSubAgentTools", childSystem)
	}
}

// scriptedSender returns a fixed sequence of replies across all sender methods,
// sharing one counter between the parent turn and any sub-agent it spawns (both
// run against the same sender). It lets a test drive a sub_agent tool_use
// and observe the sub-agent run inline.
type scriptedSender struct {
	mu      sync.Mutex
	replies []agent.Reply
	calls   int
}

func (s *scriptedSender) next() agent.Reply {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r agent.Reply
	if s.calls < len(s.replies) {
		r = s.replies[s.calls]
	} else {
		r = agent.Reply{Content: "fallback"}
	}
	s.calls++
	return r
}

func (s *scriptedSender) SendMessages(_ context.Context, _, _ string, _ []agent.Message, _ int) (agent.Reply, error) {
	return s.next(), nil
}

func (s *scriptedSender) StreamMessages(_ context.Context, _, _ string, _ []agent.Message, _ int, onChunk func(string), _ func(string)) (agent.Reply, error) {
	r := s.next()
	if onChunk != nil && r.Content != "" {
		onChunk(r.Content)
	}
	return r, nil
}

func (s *scriptedSender) SendMessagesWithTools(_ context.Context, _, _ string, _ []agent.Message, _ int, _ []agent.ToolDefinition) (agent.Reply, error) {
	return s.next(), nil
}

func (s *scriptedSender) StreamMessagesWithTools(_ context.Context, _, _ string, _ []agent.Message, _ int, _ []agent.ToolDefinition, onChunk func(string), _ agent.ToolInputDeltaFunc, _ agent.ThinkingDeltaFunc) (agent.Reply, error) {
	r := s.next()
	if onChunk != nil && r.Content != "" {
		onChunk(r.Content)
	}
	return r, nil
}

// TestServerRunsSubAgentSynchronously drives a full turn whose first reply asks
// for a sub-agent: the synchronous sub_agent path must run the child inline
// (against the same scripted sender) and feed its reply back so the turn
// finishes in one request. Three sender calls prove it: parent → child → parent.
func TestServerRunsSubAgentSynchronously(t *testing.T) {
	// Isolate HOME so the permission engine uses the embedded defaults (which
	// allow sub_agent), not a developer's ~/.octo/permissions.yml.
	t.Setenv("HOME", t.TempDir())

	sender := &scriptedSender{replies: []agent.Reply{
		// 1. Parent asks to spawn a sub-agent.
		{
			Blocks: []agent.ContentBlock{
				agent.NewToolUseBlock("tu1", "sub_agent", map[string]any{
					"description": "sub task",
					"prompt":      "do the sub task",
				}),
			},
			StopReason: "tool_use",
		},
		// 2. The sub-agent's own reply (no tools) — ends the child loop.
		{Content: "child result"},
		// 3. Parent's final answer after seeing the sub-agent result.
		{Content: "parent final answer"},
	}}

	srv := &Server{
		cfg:       Config{Tools: true},
		sender:    sender,
		model:     "stub-model",
		cwd:       t.TempDir(),
		turnLocks: map[string]*sync.Mutex{},
	}
	srv.enableSubAgentTools()
	t.Cleanup(func() {
		tools.SetDefaultSubAgentManager(nil)
		tools.SetTaskStore(nil)
	})

	sess := agent.NewSession("stub-model", "")
	reply, err := srv.runTurn(context.Background(), sess, "please use a sub-agent")
	if err != nil {
		t.Fatalf("runTurn: %v", err)
	}
	if reply != "parent final answer" {
		t.Errorf("expected the parent's final answer, got %q", reply)
	}
	if sender.calls != 3 {
		t.Errorf("expected 3 sender calls (parent, sub-agent, parent), got %d — sub-agent didn't run inline", sender.calls)
	}
}
