package server

import (
	"context"
	"fmt"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/app"
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/permission"
	"github.com/open-octo/octo-agent/internal/tools"
)

// prepareToolTurn wires the per-turn tool environment for agent a: the strict,
// non-interactive permission gate, plus the session-scoped sub-agent manager
// and task store bound to THIS turn's agent and stamped into ctx so the
// sub-agent / task tools dispatch to them rather than the process-global
// gating sentinels. The sub-agent manager runs async: a spawn's completion
// lands after the turn ends and is re-injected via the session's idle
// follow-up turn (deliverModelNote / kickIdleSteerTurn), the same channel
// background-process completions use.
//
// ctx MUST carry a session id (ctxKeySessionID) — every production caller
// (runTurn, the WS turn loop, cron ticks) stamps one before calling. A
// missing id is a wiring bug, so this fails loudly instead of silently
// building a degraded turn.
//
// Callers must build this turn's tool list with tools.DefaultToolsForCtx(ctx,
// ...) — using the ctx returned here — rather than the ctx-blind
// tools.DefaultToolsFor, so sub_agent/workflow are advertised off the
// ctx-scoped manager just stamped in above (#1133). prepareToolTurn no longer
// touches the process-global spawner/sub-agent-manager slots at all.
func (s *Server) prepareToolTurn(ctx context.Context, a *agent.Agent, sess *agent.Session) (context.Context, agent.ToolExecutor, *tools.SubAgentManager, func(), error) {
	sid, _ := ctx.Value(ctxKeySessionID{}).(string)
	if sid == "" {
		return ctx, nil, nil, func() {}, fmt.Errorf("prepareToolTurn: no session id in context — callers must stamp ctxKeySessionID")
	}

	// A session-scoped tracker (keyed by sid, cached across turns like the
	// background/sub-agent/workflow managers below) so a file read_file'd in
	// one turn is still "read" when a later turn in the same conversation
	// writes it — a per-turn tracker would forget every read as soon as the
	// turn ended.
	executor := tools.NewDefaultRegistryWithTracker(tools.SessionReadTracker(sid))

	// Goal tools dispatch to the turn's session on every tool-enabled path
	// (WS, REST, scheduled) — advertising them (SetGoalsEnabled) while
	// wiring only one path would leave the others erroring on a tool the
	// schema promised (the #597 class).
	if s.goalsEnabled.Load() && sess != nil {
		ctx = tools.WithGoalStore(ctx, sess)
	}

	// Resolve the routed agent's profile for per-turn tool/skill filtering.
	// The store is read-through, so profile edits land on the next message.
	// Stamped into ctx so DefaultToolsForCtx auto-filters via
	// DefaultToolsForProfile.
	if sess != nil {
		ctx = tools.WithProfileStore(ctx, s.agentStore)
		ctx = tools.WithSessionAgentID(ctx, sess.EffectiveAgentID())
	}

	// Gate image-handing tools (browser, read_file) on the active model's vision
	// capability. Unlike the CLI (which goes through app.WireTools), the server
	// wires tools here, so this is the only place serve learns whether the model
	// can take images — a text-only model would otherwise be handed a screenshot
	// or image block it rejects (HTTP 400). Stamped into ctx (not the
	// process-global tools.SetModelVision) so two concurrent sessions running
	// different models don't race on the setting; re-evaluated every turn so a
	// mid-session model switch takes effect. LoadCached so a config.yml that's
	// momentarily invalid mid-edit keeps the last vision setting that parsed
	// instead of silently going stale.
	cfg, cfgErr := config.LoadCached()
	if cfgErr == nil {
		ctx = tools.WithModelVision(ctx, cfg.ModelVision(a.Model))
		// A configured vision helper relaxes that gate: the tools may return
		// image blocks even to a text-only model, because agent.describeImages
		// turns them into text before the request goes out.
		_, helperOK := cfg.ResolveVisionHelper()
		ctx = tools.WithImageDescriberActive(ctx, helperOK)
	}

	// Same omission for the LLM-backed browser helpers: record_stop's
	// distillation and replay's selector self-heal need a model. WireTools
	// installs these for the CLI (via the process-global setters); serve must
	// too, but stamps them into ctx instead — same concurrent-session race as
	// vision above — or the web UI silently falls back to deterministic
	// compilation and no self-heal.
	ctx = tools.WithBrowserRecordingGenerator(ctx, app.MakeRecordingGenerator(a.GetSender(), a.Model))
	ctx = tools.WithBrowserHealer(ctx, app.MakeBrowserHealer(a.GetSender(), a.Model))

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
	engine, err := permission.New(permissionConfigPath(), a.CWD, mode, s.memoryWriteRoots()...)
	if err != nil {
		return ctx, nil, nil, func() {}, fmt.Errorf("permission engine: %w", err)
	}

	ask := s.permissionAskFrom(sid)
	engine.AttachRemembered(s.rememberedFor(sid))
	a.Gate = app.NewPermissionGate(engine, ask)

	// Clear-and-rebuild: a fully-completed plan is closed, so drop it BEFORE
	// NewSessionToolEnv picks up the per-session task store — this turn's new
	// tasks then start a fresh plan instead of piling onto old, done ones. An
	// incomplete plan carries over so the agent keeps working on it. Turns are
	// serialized per session, so this read-then-reset is safe.
	if tools.AllTasksComplete(tools.PeekSessionTaskStore(sid)) {
		tools.CloseSessionTaskStore(sid)
	}
	// Reuse the concurrency-safe core from app.NewSessionToolEnv. Server-
	// specific callbacks (WebSocket broadcast, model note delivery) are
	// injected here; the core function stays free of *Server dependencies.
	ctx, _, mgr, cleanup := app.NewSessionToolEnv(ctx, a, sid, executor, app.ToolEnvCallbacks{
		SubAgentOnEvent: func(ev tools.SubAgentEvent) {
			if s.wsHub == nil {
				return
			}
			s.wsHub.broadcast(sid, subAgentEventPayload(sid, ev))
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
			s.wsHub.broadcast(sid, workflowEventPayload(sid, ev))
		},
		WorkflowOnDone: func(ev tools.WorkflowNotification) {
			s.deliverModelNote(sid, tools.FormatWorkflowNote(ev))
		},
	})

	return ctx, executor, mgr, cleanup, nil
}

// workflowEventPayload is the single WS wire shape for one workflow runtime
// event — shared by the live broadcast above and the late-join replay in
// replayLiveState so the two can never drift.
func workflowEventPayload(sessionID string, ev tools.WorkflowEvent) map[string]any {
	return map[string]any{
		"type":        "workflow_event",
		"session_id":  sessionID,
		"run_id":      ev.RunID,
		"description": ev.Description,
		"kind":        ev.Kind,
		"line":        ev.Line,
		"status":      ev.Status,
		"agent_id":    ev.AgentID,
		"agent_label": ev.AgentLabel,
		"tool_id":     ev.ToolID,
		"tool_name":   ev.ToolName,
		"tool_input":  ev.ToolInput,
		"tool_output": ev.ToolOutput,
		"text":        ev.Text,
		"reply":       ev.Reply,
		"error":       ev.Error,
	}
}

// subAgentEventPayload is the single WS wire shape for one sub-agent runtime
// event — shared by the live broadcast above and the late-join replay in
// replayLiveState so the two can never drift.
func subAgentEventPayload(sessionID string, ev tools.SubAgentEvent) map[string]any {
	return map[string]any{
		"type":        "sub_agent_event",
		"session_id":  sessionID,
		"agent_id":    ev.AgentID,
		"description": ev.Description,
		"agent_type":  ev.AgentType,
		"kind":        ev.Kind,
		"tool_id":     ev.ToolID,
		"tool_name":   ev.ToolName,
		"tool_input":  ev.ToolInput,
		"tool_output": ev.ToolOutput,
		"text":        ev.Text,
		"stop_reason": ev.StopReason,
		"result":      ev.Result,
	}
}
