package tools

import (
	"context"
	"sync"

	"github.com/open-octo/octo-agent/internal/agent"
)

// Spawner runs one sub-agent task to completion and returns the final reply.
// Implementations live outside the tools package (cmd/octo wires the real one,
// tests substitute fakes) so this package stays free of agent-construction
// concerns. Multiple Spawner calls may run concurrently from one parent
// tool_use batch (see parallel dispatch in agent.dispatchTools), so the
// implementation MUST be safe for concurrent invocation on distinct requests.
type Spawner interface {
	Spawn(ctx context.Context, req SpawnRequest) (SpawnResult, error)
	// Continue re-runs a still-alive sub-agent (one a prior Spawn kept in the
	// implementation's live registry) with a new message and returns its next
	// reply. The sub-agent's prior history carries over. An unknown or
	// already-evicted agentID returns an error whose text tells the model to
	// launch a fresh sub-agent instead. Concurrent Continue calls on the SAME
	// agentID must be serialized by the implementation — a sub-agent's history
	// can't take two interleaved turns.
	Continue(ctx context.Context, agentID, message string) (SpawnResult, error)
}

// SpawnRequest is the LLM-supplied Agent tool payload, parsed.
type SpawnRequest struct {
	// Description is a short human-readable label for logging / progress UI.
	Description string

	// AgentType is the subagent_type the caller selected (e.g. "explore",
	// "general"), shown alongside Description in the live panels. Empty for an
	// untyped fork.
	AgentType string

	// Prompt is the sub-agent's user message. It carries the task.
	Prompt string

	// ForkConversation, when true, seeds the child's history with the parent's
	// conversation so far (a true fork), rather than starting fresh. The
	// spawner trims the in-flight tool_use turn that spawned the child so the
	// copied history ends cleanly. Set by the sub_agent tool when no
	// subagent_type is given; workflow agents leave it false.
	ForkConversation bool

	// ForkHistory, when non-nil, is the parent-conversation snapshot the fork
	// child is seeded with. The sub_agent tool captures it at tool-execution
	// time (via ForkSnapshotter) so the fork reflects the conversation as it
	// stood when the model made the call — a background spawn's goroutine may
	// run arbitrarily later, after the parent turn has appended its own
	// "Started sub-agent…" results and follow-up messages, which a child must
	// not mistake for its own role. Nil means "snapshot inside Spawn" (only
	// safe for synchronous callers).
	ForkHistory []agent.Message

	// Tools, when non-empty, restricts the child to this subset of the
	// parent's tool list. nil/empty means "inherit all of parent's tools
	// except Agent itself" — the spawn implementation handles the
	// recursion filter, not the tool.
	Tools []string

	// DisallowedTools is subtracted from the child's inherited tool set
	// (frontmatter `disallowed_tools`). Applied on top of Tools/ReadOnly.
	DisallowedTools []string

	// Model, when non-empty, overrides the parent's model for this child.
	Model string

	// SystemSuffix, when non-empty, is appended to the child's system prompt
	// (after the shared parent System) to give a preset agent its persona.
	SystemSuffix string

	// ReadOnly, when true, strips the mutating tools (write_file, edit_file)
	// from the child's toolbelt on top of the always-dropped Agent tool.
	ReadOnly bool

	// LeanSystem, when true, seeds the child with the parent's lean system
	// prompt (skills manifest + memory dropped) to keep its context small.
	// Falls back to the parent's full system when none is configured. It only
	// trims context — the child always runs on the parent's model (or an
	// explicit Model override); model quality is never traded for cost.
	LeanSystem bool

	// Schema, when non-empty, is a JSON Schema (as a JSON string) the child's
	// reply must satisfy. The spawner instructs the child to emit only matching
	// JSON, strips any markdown fences, and re-prompts once if the reply isn't
	// valid JSON. The returned Reply is the cleaned JSON text.
	Schema string

	// Isolation, when "worktree", runs the child in a fresh git worktree so its
	// file/terminal changes don't touch the main checkout. Changes are left on a
	// dedicated branch for the caller to review; an unchanged run is cleaned up.
	// Requires a git repository.
	Isolation string

	// SessionDir, when non-empty, tells the spawner to persist the sub-agent's
	// full conversation transcript to <SessionDir>/<agent-id>.jsonl so it can
	// be inspected after a failure.
	SessionDir string
}

// ForkSnapshotter is implemented by Spawner implementations that can capture
// the parent conversation synchronously. The sub_agent tool calls it on the
// tool-execution path — while the parent turn is still parked on this tool
// call — so a background fork seeds from the conversation the model actually
// saw, not from wherever the parent turn has advanced to by the time the
// spawn goroutine gets scheduled.
type ForkSnapshotter interface {
	ForkSnapshot() []agent.Message
}

// SpawnResult is the sub-agent's final output, plus its token usage so the
// parent can roll it into the session total.
type SpawnResult struct {
	// AgentID addresses the sub-agent for a later Continue. Spawn returns
	// a non-empty id when the implementation keeps the child alive.
	AgentID      string
	Reply        string
	InputTokens  int
	OutputTokens int
	// Turns is the number of provider round-trips the sub-agent executed.
	Turns int
	// StopReason carries why the sub-agent stopped: a provider sentinel
	// ("end_turn"/"tool_use"/…) for a normal completion, "max_turns"/"max_tokens"
	// when a budget was exhausted, and empty when the run errored out.
	StopReason string
}

// activeSpawner, when non-nil, backs the Agent tool and gates its
// advertisement in DefaultTools. Set once at session start via SetSpawner.
// Nil disables sub-agent dispatch — the tool stays out of the LLM-facing
// schema and Execute errors.
var (
	activeSpawnerMu sync.RWMutex
	activeSpawner   Spawner
)

// SetSpawner registers the function the Agent tool delegates to. Pass
// nil to disable (the tool then doesn't appear in DefaultTools).
func SetSpawner(s Spawner) {
	activeSpawnerMu.Lock()
	activeSpawner = s
	activeSpawnerMu.Unlock()
}

// ActiveSpawner returns the currently registered Spawner, or nil if none.
func ActiveSpawner() Spawner {
	activeSpawnerMu.RLock()
	defer activeSpawnerMu.RUnlock()
	return activeSpawner
}

func spawnerEnabled() bool {
	activeSpawnerMu.RLock()
	defer activeSpawnerMu.RUnlock()
	return activeSpawner != nil
}

// subAgentCtxKey marks a context as belonging to a sub-agent's run. The
// Agent tool checks for it to refuse recursive nesting.
type subAgentCtxKeyType struct{}

var subAgentCtxKey = subAgentCtxKeyType{}

// WithSubAgentMarker stamps ctx so descendants are detectable as sub-agent
// work. The Spawner implementation calls this when invoking the child loop.
func WithSubAgentMarker(ctx context.Context) context.Context {
	return context.WithValue(ctx, subAgentCtxKey, true)
}

// IsSubAgent reports whether ctx is currently inside a sub-agent's run.
func IsSubAgent(ctx context.Context) bool {
	v, _ := ctx.Value(subAgentCtxKey).(bool)
	return v
}

// withAgentTag prefixes a sub-agent reply with "[agent <id>] " so the parent
// model has a stable handle to address in a follow-up Agent call.
func withAgentTag(id, reply string) string {
	if id == "" {
		return reply
	}
	return "[agent " + id + "] " + reply
}
