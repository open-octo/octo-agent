package app

import (
	"context"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// ToolEnvCallbacks carries optional hooks for sub-agent and workflow lifecycle
// events. All fields may be nil.
type ToolEnvCallbacks struct {
	SubAgentOnEvent func(tools.SubAgentEvent)
	SubAgentOnExit  func(tools.SubAgentNotification)
	WorkflowOnEvent func(tools.WorkflowEvent)
	WorkflowOnDone  func(tools.WorkflowNotification)
}

// NewSessionToolEnv builds a session-scoped, concurrency-safe tool environment
// for one turn. It returns a context stamped with the per-session managers, a
// fresh *tools.DefaultRegistry, and the SubAgentManager that was wired into
// the context.
//
// Contracts:
//   - It does NOT read or write Agent.Gate. Callers set a.Gate before or after
//     this call.
//   - It does NOT call config.Load() or permission.New(). It performs no local
//     file I/O.
//   - It does NOT handle process-global browser state (SetBrowserVision etc.) or
//     workflow-discovery-cwd state. See dev-docs/octoagent-pkg-design.md.
//   - It does NOT register or use an MCP registry.
//
// The returned cleanup function is currently a no-op but is reserved for future
// resource release. It does NOT destroy the session-scoped managers (sub-agent,
// background, workflow), because those are cached by sessionID and reused across
// turns. Callers manage session lifecycle resources themselves.
func NewSessionToolEnv(
	ctx context.Context,
	a *agent.Agent,
	sessionID string,
	executor agent.ToolExecutor,
	callbacks ToolEnvCallbacks,
) (context.Context, agent.ToolExecutor, *tools.SubAgentManager, func()) {
	ctx = tools.WithWorkingDir(ctx, a.CWD)
	ctx = tools.WithBackgroundManager(ctx, tools.SessionBackgroundManager(sessionID))
	// Per-session task store, cached by sessionID like the managers above, so the
	// task/plan checklist survives across turns and a page refresh (a per-turn
	// tasks.New() here is why the panel used to vanish). Callers that want a plan
	// reset between turns do it via CloseSessionTaskStore before this call.
	ctx = tools.WithTaskStore(ctx, tools.SessionTaskStore(sessionID))

	mkSpawner := func() tools.Spawner {
		return NewSpawner(a, executor, func(ctx context.Context) []agent.ToolDefinition {
			return tools.DefaultToolsForCtx(ctx, a.Model)
		})
	}
	mgr := tools.SessionSubAgentManager(sessionID, mkSpawner)
	if callbacks.SubAgentOnEvent != nil {
		mgr.SetOnEvent(callbacks.SubAgentOnEvent)
	}
	if callbacks.SubAgentOnExit != nil {
		mgr.SetOnExit(callbacks.SubAgentOnExit)
	}
	ctx = tools.WithSubAgentManager(ctx, mgr)

	wfMgr := tools.SessionWorkflowManager(sessionID)
	if callbacks.WorkflowOnEvent != nil {
		wfMgr.SetOnEvent(callbacks.WorkflowOnEvent)
	}
	if callbacks.WorkflowOnDone != nil {
		wfMgr.SetOnDone(callbacks.WorkflowOnDone)
	}
	ctx = tools.WithWorkflowManager(ctx, wfMgr)

	cleanup := func() {}
	return ctx, executor, mgr, cleanup
}
