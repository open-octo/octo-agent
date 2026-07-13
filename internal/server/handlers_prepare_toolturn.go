package server

import (
	"context"
	"fmt"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/app"
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/permission"
	"github.com/open-octo/octo-agent/internal/tasks"
	"github.com/open-octo/octo-agent/internal/tools"
)

// prepareToolTurn wires the per-turn tool environment for agent a: the strict,
// non-interactive permission gate, plus a sub-agent manager and task store
// bound to THIS turn's agent and stamped into ctx so the sub-agent / task tools
// dispatch to them rather than the process-global gating sentinels. The manager
// runs synchronously — a request/response turn has no follow-up channel for an
// async sub-agent result — and each turn gets a private store, so concurrent
// sessions never share sub-agent or task state.
//
// Callers must build this turn's tool list with tools.DefaultToolsForCtx(ctx,
// ...) — using the ctx returned here — rather than the ctx-blind
// tools.DefaultToolsFor, so sub_agent/workflow are advertised off the
// ctx-scoped manager just stamped in above (#1133). prepareToolTurn no longer
// touches the process-global spawner/sub-agent-manager slots at all; the
// returned cleanup only restores the separate ActiveWorkflowDiscoveryCWD swap
// (see its doc comment — a different concern, since WorkflowTool.Definition()
// has no ctx to read a.CWD from).
func (s *Server) prepareToolTurn(ctx context.Context, a *agent.Agent, sess *agent.Session) (context.Context, agent.ToolExecutor, *tools.SubAgentManager, func(), error) {
	sid, hasSession := ctx.Value(ctxKeySessionID{}).(string)

	// A session-scoped tracker (keyed by sid, cached across turns like the
	// background/sub-agent/workflow managers below) so a file read_file'd in
	// one turn is still "read" when a later turn in the same conversation
	// writes it — a per-turn tracker would forget every read as soon as the
	// turn ended. One-shot paths with no session identity keep a fresh
	// per-call tracker (nothing to persist it against).
	var executor tools.DefaultRegistry
	if hasSession && sid != "" {
		executor = tools.NewDefaultRegistryWithTracker(tools.SessionReadTracker(sid))
	} else {
		executor = tools.NewDefaultRegistry()
	}

	// Goal tools dispatch to the turn's session on every tool-enabled path
	// (WS, REST, scheduled) — advertising them (SetGoalsEnabled) while
	// wiring only one path would leave the others erroring on a tool the
	// schema promised (the #597 class).
	if s.goalsEnabled.Load() && sess != nil {
		ctx = tools.WithGoalStore(ctx, sess)
	}

	// Gate browser image content on the active model's vision capability. Unlike
	// the CLI (which goes through app.WireTools), the server wires tools here, so
	// this is the only place serve learns whether the model can take images — a
	// text-only model would otherwise be handed a screenshot it rejects (HTTP
	// 400). Re-evaluated per turn so a mid-session model switch takes effect.
	// LoadCached so a config.yml that's momentarily invalid mid-edit keeps
	// the last vision setting that parsed instead of silently going stale.
	cfg, cfgErr := config.LoadCached()
	if cfgErr == nil {
		tools.SetBrowserVision(cfg.ModelVision(a.Model))
	}

	// Same omission for the LLM-backed browser helpers: record_stop's skill
	// distillation and run_skill's selector self-heal need a model. WireTools
	// installs these for the CLI; serve must too, or the web UI silently falls
	// back to deterministic compilation and no self-heal.
	tools.SetBrowserSkillGenerator(app.MakeSkillGenerator(a.GetSender(), a.Model))
	tools.SetBrowserHealer(app.MakeBrowserHealer(a.GetSender(), a.Model))

	// Same omission for the external memory backend: WireTools installs it for
	// the CLI, but serve never calls WireTools. app.RefreshMemoryBackend is
	// also called earlier in the same turn by buildAgent/runChannelTurns
	// (before they read tools.MemoryBackendGuidance()/call
	// tools.RegisterMemoryBackendHooks, which need the refreshed globals) —
	// calling it again here is redundant but harmless, and keeps this path
	// correct standalone if ever called without one of those two upstream.
	app.RefreshMemoryBackend()

	// Anchor the gate at the agent's per-session cwd (not the server default) so
	// $CWD path rules and relative-path resolution match where the tools
	// actually run — buildAgent sets a.CWD from sess.WorkingDir before every
	// prepareToolTurn call, cron-scheduled sessions included (task.Directory
	// only ever seeds sess.WorkingDir once, at session creation).
	mode := resolvePermissionMode()
	if sess != nil && sess.PermissionMode != "" {
		mode = permission.Mode(sess.PermissionMode)
	}
	if sess != nil && sess.Source == "cron" && mode == permission.ModeInteractive {
		// interactive was never functional for a cron tick — nobody is present
		// to answer the ask, so it only ever hangs and denies. This also
		// self-heals task sessions created before write_file/edit_file stopped
		// blanket-allowing $CWD: CreateSession used to persist whatever the
		// global default resolved to at creation time (often "interactive"),
		// and that value lives on in ~/.octo/sessions/*.json across upgrades —
		// tasks_handlers.go's CreateSession only sets PermissionMode for a
		// session it creates, never for one it reuses, so an old task would
		// otherwise be stuck denying every write forever.
		mode = permission.ResolveUnattendedDefaultMode()
	}
	engine, err := permission.New(permissionConfigPath(), a.CWD, mode, s.memDir, s.homeMemDir)
	if err != nil {
		return ctx, nil, nil, func() {}, fmt.Errorf("permission engine: %w", err)
	}

	var ask app.PermissionAsk
	if hasSession && sid != "" {
		ask = s.permissionAskFrom(sid)
		engine.AttachRemembered(s.rememberedFor(sid))
	}
	a.Gate = app.NewPermissionGate(engine, ask)

	mkSpawner := func() tools.Spawner {
		return app.NewSpawner(a, executor, func(ctx context.Context) []agent.ToolDefinition {
			return tools.DefaultToolsForCtx(ctx, a.Model)
		})
	}

	var mgr *tools.SubAgentManager
	var cleanup func()

	if hasSession && sid != "" {
		// Session-scoped path: reuse the concurrency-safe core from app.NewSessionToolEnv.
		// Server-specific callbacks (WebSocket broadcast, model note delivery) are
		// injected here; the core function stays free of *Server dependencies.
		ctx, _, mgr, cleanup = app.NewSessionToolEnv(ctx, a, sid, executor, app.ToolEnvCallbacks{
			SubAgentOnEvent: func(ev tools.SubAgentEvent) {
				if s.wsHub == nil {
					return
				}
				s.wsHub.broadcast(sid, map[string]any{
					"type":        "sub_agent_event",
					"session_id":  sid,
					"agent_id":    ev.AgentID,
					"description": ev.Description,
					"agent_type":  ev.AgentType,
					"kind":        ev.Kind,
					"tool_name":   ev.ToolName,
					"tool_input":  ev.ToolInput,
				})
			},
			SubAgentOnExit: func(ev tools.SubAgentNotification) {
				if s.wsHub == nil {
					return
				}
				s.wsHub.broadcast(sid, wsEventSubAgentNotice{
					Type:        "sub_agent_notice",
					SessionID:   sid,
					AgentID:     ev.AgentID,
					Description: ev.Description,
					Kind:        ev.Kind,
					Status:      subAgentNoticeStatus(ev),
				})
				s.notifySubAgentExit(sid, ev)
			},
			WorkflowOnEvent: func(ev tools.WorkflowEvent) {
				if s.wsHub == nil {
					return
				}
				s.wsHub.broadcast(sid, map[string]any{
					"type":        "workflow_event",
					"session_id":  sid,
					"run_id":      ev.RunID,
					"description": ev.Description,
					"kind":        ev.Kind,
					"line":        ev.Line,
					"status":      ev.Status,
				})
			},
			WorkflowOnDone: func(ev tools.WorkflowNotification) {
				s.deliverModelNote(sid, tools.FormatWorkflowNote(ev))
			},
		})
	} else {
		// No session identity (one-shot runTurn paths): keep the old
		// request/response semantics — block on every sub-agent. Deliberately
		// leave ctx without a background manager, same as before this
		// function was split: resolveBackgroundManager's fallback to
		// defaultBg is exactly the "no ctx-scoped manager" case it documents,
		// and stamping tools.SessionBackgroundManager("") here would silently
		// swap that documented fallback for an undocumented synthetic
		// "" session cached forever in sessionMgrs.
		ctx = tools.WithWorkingDir(ctx, a.CWD)
		ctx = tools.WithTaskStore(ctx, tasks.New())
		mgr = tools.NewSubAgentManager(mkSpawner())
		mgr.SetSynchronous(true)
		ctx = tools.WithSubAgentManager(ctx, mgr)
		cleanup = func() {}
	}

	// The workflow tool's Definition(), unlike its Execute, takes no ctx and
	// so can't see a.CWD directly — that one remains a save-and-restore
	// process-global swap (a separate concern from advertisement gating; see
	// workflow.go's ActiveWorkflowDiscoveryCWD doc comment).
	prevWorkflowDiscoveryCWD := tools.ActiveWorkflowDiscoveryCWD()
	tools.SetWorkflowDiscoveryCWD(a.CWD)
	prevCleanup := cleanup
	cleanup = func() {
		prevCleanup()
		tools.SetWorkflowDiscoveryCWD(prevWorkflowDiscoveryCWD)
	}

	return ctx, executor, mgr, cleanup, nil
}
