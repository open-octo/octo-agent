package server

import (
	"context"
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
	srv := &Server{
		cfg:       Config{Tools: true},
		sender:    &stubSender{},
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

	if g := tools.MemoryBackendGuidance(); g == "" {
		t.Error("enableSubAgentTools should refresh the memory backend from config before reading MemoryBackendGuidance(), got empty guidance")
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
