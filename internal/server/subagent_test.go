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
	for _, d := range tools.DefaultToolsFor("", 0) {
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
// (unlike buildAgent/runChannelTurns, which each compose once per session and
// freeze — see Session.SetComposedSystem — rather than reusing one shared
// template). Without
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
		Endpoints: []config.Endpoint{{ID: "ep-a", Provider: "openai", Models: []config.EndpointModel{{Model: "gpt-4o"}}}},
		Default:   "ep-a::gpt-4o",
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

// scriptedSender hands one conversation — the parent turn, named by the user
// message it opens with — a fixed sequence of replies, and answers every other
// conversation with a constant.
//
// Routing on the conversation rather than on a shared counter is what makes it
// deterministic. A sub-agent the turn spawns runs against this same sender on
// its own goroutine, so with one counter the child's first call could land
// between the parent's two and take the reply the parent was about to get,
// leaving the parent with the fallback. Which one got there first was the
// runner's decision, not the test's.
type scriptedSender struct {
	mu sync.Mutex
	// opening is the parent turn's first user message; a conversation that
	// starts with anything else is somebody else's.
	opening string
	replies []agent.Reply
	calls   int
}

func (s *scriptedSender) next(msgs []agent.Message) agent.Reply {
	if !s.isParent(msgs) {
		return agent.Reply{Content: "child reply"}
	}
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

// isParent reports whether these messages are the parent turn's conversation,
// by the user message it opens with — the one thing that stays put as the turn
// grows tool_use and tool_result messages on the end.
func (s *scriptedSender) isParent(msgs []agent.Message) bool {
	for _, m := range msgs {
		if m.Role == agent.RoleUser {
			return m.Content == s.opening
		}
	}
	return false
}

func (s *scriptedSender) SendMessages(_ context.Context, _, _ string, msgs []agent.Message, _ int) (agent.Reply, error) {
	return s.next(msgs), nil
}

func (s *scriptedSender) StreamMessages(_ context.Context, _, _ string, msgs []agent.Message, _ int, onChunk func(string), _ func(string)) (agent.Reply, error) {
	r := s.next(msgs)
	if onChunk != nil && r.Content != "" {
		onChunk(r.Content)
	}
	return r, nil
}

func (s *scriptedSender) SendMessagesWithTools(_ context.Context, _, _ string, msgs []agent.Message, _ int, _ []agent.ToolDefinition) (agent.Reply, error) {
	return s.next(msgs), nil
}

func (s *scriptedSender) StreamMessagesWithTools(_ context.Context, _, _ string, msgs []agent.Message, _ int, _ []agent.ToolDefinition, onChunk func(string), _ agent.ToolInputDeltaFunc, _ agent.ThinkingDeltaFunc) (agent.Reply, error) {
	r := s.next(msgs)
	if onChunk != nil && r.Content != "" {
		onChunk(r.Content)
	}
	return r, nil
}

// TestServerBackgroundsSubAgent drives a full turn whose first reply asks for
// a sub-agent. A server turn can deliver a completion notification as a
// follow-up turn, so the child is dispatched to the background: the
// tool_result the parent sees is a handle, and the turn finishes without
// waiting for the child's own reply.
func TestServerBackgroundsSubAgent(t *testing.T) {
	// Isolate HOME so the permission engine uses the embedded defaults (which
	// allow sub_agent), not a developer's ~/.octo/permissions.yml.
	t.Setenv("HOME", t.TempDir())

	const ask = "please use a sub-agent"
	sender := &scriptedSender{opening: ask, replies: []agent.Reply{
		// 1. Parent asks to spawn a sub-agent.
		{
			Blocks: []agent.ContentBlock{
				agent.NewToolUseBlock("tu1", "sub_agent", map[string]any{
					"description":   "sub task",
					"prompt":        "do the sub task",
					"subagent_type": "general",
				}),
			},
			StopReason: "tool_use",
		},
		// 2. The parent's answer once it holds the handle — it does not wait
		// for the child. The background child draws from this same sender
		// afterwards, so the turn under test must not depend on what it gets.
		{Content: "dispatched, will report back"},
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
	reply, err := srv.runTurn(context.Background(), sess, ask)
	if err != nil {
		t.Fatalf("runTurn: %v", err)
	}
	if reply != "dispatched, will report back" {
		t.Errorf("expected the parent to finish without waiting, got %q", reply)
	}

	// The tool_result must be the background handle. Asserting on the text
	// rather than counting sender calls keeps this deterministic: the child
	// runs concurrently and draws from the same sender on its own schedule.
	var toolResult string
	for _, msg := range sess.Messages {
		for _, b := range msg.Blocks {
			if b.Type == "tool_result" {
				toolResult = b.Result
			}
		}
	}
	if toolResult == "" {
		t.Fatal("the turn recorded no tool_result for the sub_agent call")
	}
	if !strings.Contains(toolResult, "Started sub-agent") {
		t.Errorf("server turns dispatch sub-agents in the background; tool_result = %q", toolResult)
	}
	if strings.Contains(toolResult, "child result") {
		t.Errorf("the parent must not block for the child's reply; tool_result = %q", toolResult)
	}
}
