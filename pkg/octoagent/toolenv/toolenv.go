// Package toolenv wires a concurrency-safe, session-scoped tool environment for
// external consumers of pkg/octoagent.
//
// It aliases internal/app.NewSessionToolEnv and exposes optional callbacks for
// sub-agent and workflow events.
package toolenv

import (
	"context"

	"github.com/open-octo/octo-agent/internal/app"
	"github.com/open-octo/octo-agent/internal/tools"
	"github.com/open-octo/octo-agent/pkg/octoagent"
)

// SubAgentEvent is a runtime progress event from a sub-agent.
type SubAgentEvent = tools.SubAgentEvent

// SubAgentNotification is delivered when a sub-agent finishes.
type SubAgentNotification = tools.SubAgentNotification

// WorkflowEvent is a runtime progress event from a background workflow.
type WorkflowEvent = tools.WorkflowEvent

// WorkflowNotification is delivered when a background workflow finishes.
type WorkflowNotification = tools.WorkflowNotification

// Option customizes the tool environment wired by WireForSession.
type Option func(*options)

type options struct {
	subAgentOnEvent func(SubAgentEvent)
	subAgentOnExit  func(SubAgentNotification)
	workflowOnEvent func(WorkflowEvent)
	workflowOnDone  func(WorkflowNotification)
}

// WithSubAgentEvents wires callbacks for sub-agent lifecycle events.
// onEvent fires on each runtime progress event; onExit fires once when the
// sub-agent finishes. Either may be nil.
func WithSubAgentEvents(
	onEvent func(SubAgentEvent),
	onExit func(SubAgentNotification),
) Option {
	return func(o *options) {
		o.subAgentOnEvent = onEvent
		o.subAgentOnExit = onExit
	}
}

// WithWorkflowEvents wires callbacks for workflow lifecycle events.
// onEvent fires on each runtime progress event; onDone fires once when the
// workflow finishes. Either may be nil.
func WithWorkflowEvents(
	onEvent func(WorkflowEvent),
	onDone func(WorkflowNotification),
) Option {
	return func(o *options) {
		o.workflowOnEvent = onEvent
		o.workflowOnDone = onDone
	}
}

// WireForSession prepares a concurrency-safe tool execution environment for a
// single turn. It returns a context carrying the per-session managers, a fresh
// ToolExecutor, and a cleanup function.
//
// The sessionID is used to isolate background processes, sub-agent registries,
// and workflow managers across concurrent sessions. The same sessionID reuses
// the same cached managers.
//
// Contracts:
//   - It does NOT read or write a.Gate. Set a.Gate before or after this call.
//   - It does NOT call config.Load() or permission.New(). It performs no local
//     file I/O.
//   - It does NOT handle process-global browser state (SetBrowserVision etc.) or
//     workflow-discovery-cwd state.
//   - It does NOT register or use an MCP registry.
//
// The returned cleanup function is reserved for future resource release and is
// currently a no-op. It does NOT destroy session-scoped managers, which are
// cached by sessionID and reused across turns.
func WireForSession(
	ctx context.Context,
	a *octoagent.Agent,
	sessionID string,
	opts ...Option,
) (context.Context, octoagent.ToolExecutor, func()) {
	cfg := &options{}
	for _, opt := range opts {
		opt(cfg)
	}
	executor := tools.NewDefaultRegistry()
	ctx, _, _, cleanup := app.NewSessionToolEnv(ctx, a, sessionID, executor, app.ToolEnvCallbacks{
		SubAgentOnEvent: cfg.subAgentOnEvent,
		SubAgentOnExit:  cfg.subAgentOnExit,
		WorkflowOnEvent: cfg.workflowOnEvent,
		WorkflowOnDone:  cfg.workflowOnDone,
	})
	return ctx, executor, cleanup
}
