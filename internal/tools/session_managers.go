package tools

import (
	"context"
	"sync"
)

// backgroundManagerCtxKey scopes a BackgroundManager to a turn's context, the
// same way WithSubAgentManager / WithTaskStore scope theirs. The web server and
// IM bridge stamp a per-session manager so each conversation's background
// processes are isolated — their own bg_N id namespace, invisible to other
// sessions' terminal_output / kill_shell — and are reaped when the session
// ends, instead of sharing the process-global defaultBg.
type backgroundManagerCtxKey struct{}

// WithBackgroundManager returns ctx carrying mgr as the manager the terminal
// tools dispatch to for this turn.
func WithBackgroundManager(ctx context.Context, mgr *BackgroundManager) context.Context {
	return context.WithValue(ctx, backgroundManagerCtxKey{}, mgr)
}

func backgroundManagerFromContext(ctx context.Context) *BackgroundManager {
	m, _ := ctx.Value(backgroundManagerCtxKey{}).(*BackgroundManager)
	return m
}

// resolveBackgroundManager picks the manager a terminal tool should use: the
// ctx-scoped per-session one (web/IM) first, then a tool-local override, then
// the process-global default (CLI/TUI, which never stamp a ctx manager).
func resolveBackgroundManager(ctx context.Context, local *BackgroundManager) *BackgroundManager {
	if m := backgroundManagerFromContext(ctx); m != nil {
		return m
	}
	if local != nil {
		return local
	}
	return defaultBg
}

// Per-session background managers, keyed by an opaque session id chosen by the
// caller (web session id / IM session key). Created on demand, reaped either
// when the session is deleted (CloseSessionBackgroundManager) or on daemon
// shutdown (KillAllBackground reaps all of them). Kept separate from defaultBg
// so the CLI/TUI — which never stamp a ctx manager — are unaffected.
var (
	sessionMgrsMu sync.Mutex
	sessionMgrs   = map[string]*BackgroundManager{}
)

// SessionBackgroundManager returns the per-session manager for id, creating and
// registering it on first use.
func SessionBackgroundManager(id string) *BackgroundManager {
	sessionMgrsMu.Lock()
	defer sessionMgrsMu.Unlock()
	m := sessionMgrs[id]
	if m == nil {
		m = NewBackgroundManager()
		sessionMgrs[id] = m
	}
	return m
}

// CloseSessionBackgroundManager kills every process tracked for a session and
// drops its manager. Call when a session is deleted so its background daemons
// don't leak until daemon shutdown. No-op for an unknown id.
func CloseSessionBackgroundManager(id string) {
	sessionMgrsMu.Lock()
	m := sessionMgrs[id]
	delete(sessionMgrs, id)
	sessionMgrsMu.Unlock()
	if m != nil {
		m.KillAll()
	}
}

// Per-session sub-agent managers, keyed the same way as the background
// managers. Unlike the per-turn managers the server used to build, these
// persist across turns, which is what makes async sub-agents possible there:
// a spawn outlives the turn that launched it, its completion hook still has a
// home, and the spawner's child registry keeps children resumable in later
// turns. Reaped on session delete (CloseSessionSubAgentManager) or daemon
// shutdown (KillAllSessionSubAgents).
var (
	sessionSubMgrsMu sync.Mutex
	sessionSubMgrs   = map[string]*SubAgentManager{}
)

// SessionSubAgentManager returns the per-session sub-agent manager for id,
// creating it via mkSpawner on first use. Subsequent calls reuse the existing
// manager (and its spawner), so mkSpawner is only invoked once per session.
// If mkSpawner is nil and no manager exists for id, nil is returned — callers
// that only need to signal an already-running sync sub-agent (e.g. a WebSocket
// promote message) can use this safely without constructing a manager.
func SessionSubAgentManager(id string, mkSpawner func() Spawner) *SubAgentManager {
	sessionSubMgrsMu.Lock()
	defer sessionSubMgrsMu.Unlock()
	m := sessionSubMgrs[id]
	if m == nil && mkSpawner != nil {
		m = NewSubAgentManager(mkSpawner())
		sessionSubMgrs[id] = m
	}
	return m
}

// CloseSessionSubAgentManager kills every sub-agent tracked for a session and
// drops its manager. No-op for an unknown id.
func CloseSessionSubAgentManager(id string) {
	sessionSubMgrsMu.Lock()
	m := sessionSubMgrs[id]
	delete(sessionSubMgrs, id)
	sessionSubMgrsMu.Unlock()
	if m != nil {
		m.KillAll()
	}
}

// KillAllSessionSubAgents terminates every sub-agent across all sessions.
// Called on daemon shutdown, mirroring KillAllBackground.
func KillAllSessionSubAgents() {
	sessionSubMgrsMu.Lock()
	mgrs := make([]*SubAgentManager, 0, len(sessionSubMgrs))
	for _, m := range sessionSubMgrs {
		mgrs = append(mgrs, m)
	}
	sessionSubMgrs = map[string]*SubAgentManager{}
	sessionSubMgrsMu.Unlock()
	for _, m := range mgrs {
		m.KillAll()
	}
}

// ─── Workflow managers ──────────────────────────────────────────────────────

// defaultWorkflowMgr is the process-global background-workflow manager used by
// the CLI/TUI, which never stamp a ctx-scoped manager.
var defaultWorkflowMgr = NewWorkflowManager()

// SetDefaultWorkflowOnDone wires the completion hook on the process-global
// workflow manager so a finished background run reaches the CLI/TUI agent
// (parity with SetBackgroundOnExit / SubAgentManager.SetOnExit). Pass nil to clear.
func SetDefaultWorkflowOnDone(fn func(WorkflowNotification)) { defaultWorkflowMgr.SetOnDone(fn) }

// KillDefaultWorkflows cancels every background workflow on the process-global
// manager — called on CLI/TUI exit so a detached run doesn't linger.
func KillDefaultWorkflows() { defaultWorkflowMgr.KillAll() }

type workflowManagerCtxKey struct{}

// WithWorkflowManager stamps ctx with the per-session workflow manager the
// workflow tools dispatch to (web/IM). CLI/TUI leave it unset and fall back to
// defaultWorkflowMgr.
func WithWorkflowManager(ctx context.Context, mgr *WorkflowManager) context.Context {
	return context.WithValue(ctx, workflowManagerCtxKey{}, mgr)
}

func workflowManagerFromContext(ctx context.Context) *WorkflowManager {
	m, _ := ctx.Value(workflowManagerCtxKey{}).(*WorkflowManager)
	return m
}

// resolveWorkflowManager picks the ctx-scoped per-session manager (web/IM)
// first, else the process-global default (CLI/TUI).
func resolveWorkflowManager(ctx context.Context) *WorkflowManager {
	if m := workflowManagerFromContext(ctx); m != nil {
		return m
	}
	return defaultWorkflowMgr
}

// Per-session workflow managers, keyed like the background/sub-agent managers.
var (
	sessionWorkflowMgrsMu sync.Mutex
	sessionWorkflowMgrs   = map[string]*WorkflowManager{}
)

// SessionWorkflowManager returns the per-session workflow manager for id,
// creating it on first use.
func SessionWorkflowManager(id string) *WorkflowManager {
	sessionWorkflowMgrsMu.Lock()
	defer sessionWorkflowMgrsMu.Unlock()
	m := sessionWorkflowMgrs[id]
	if m == nil {
		m = NewWorkflowManager()
		sessionWorkflowMgrs[id] = m
	}
	return m
}

// CloseSessionWorkflowManager cancels every workflow tracked for a session and
// drops its manager. No-op for an unknown id.
func CloseSessionWorkflowManager(id string) {
	sessionWorkflowMgrsMu.Lock()
	m := sessionWorkflowMgrs[id]
	delete(sessionWorkflowMgrs, id)
	sessionWorkflowMgrsMu.Unlock()
	if m != nil {
		m.KillAll()
	}
}

// allBackgroundManagers returns defaultBg plus every live per-session manager,
// so process-wide operations (shutdown reap) cover every tracked process.
func allBackgroundManagers() []*BackgroundManager {
	sessionMgrsMu.Lock()
	defer sessionMgrsMu.Unlock()
	out := make([]*BackgroundManager, 0, len(sessionMgrs)+1)
	out = append(out, defaultBg)
	for _, m := range sessionMgrs {
		out = append(out, m)
	}
	return out
}
