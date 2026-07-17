package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/tools"
)

// sessionLiveState tracks in-progress agent state for replay to late subscribers.
type sessionLiveState struct {
	progress    *wsEventProgress
	stdoutLines []string
	// stdoutToolID is the tool_id stdoutLines belongs to, so a late-
	// subscribing tab's replayed tool_stdout can attribute the lines to the
	// right card (see #1193's pickToolIndex on the frontend).
	stdoutToolID string

	// events buffers the turn's already-broadcast transcript events
	// (tool_call / tool_result / tool_error / steer history_user_message,
	// plus flushed deltas) so a tab that subscribes mid-turn — e.g. after a
	// page refresh — can replay what it missed. The session file only gains
	// the turn's messages at turn end, so until then this buffer is the only
	// source. Dropped with the live state once the turn persists.
	events []map[string]any

	// textBuf / thinkingBuf accumulate the in-flight LLM round's streamed
	// deltas. flushDeltas folds them into events when the round ends in a
	// tool call; anything still unflushed is replayed directly after events.
	textBuf     strings.Builder
	thinkingBuf strings.Builder

	// historyWatermark is how many persisted messages predate this turn
	// (including the turn's own user message). The turn's progress is saved
	// to disk incrementally, so without a cut-off a mid-turn history fetch
	// would reconstruct the same rounds the replay buffer resends — every
	// tool card twice. The history endpoint serves messages below the
	// watermark; the buffer owns everything above it. Mid-turn compaction
	// shifts it (see the EventCompactDone handler). 0 means unset (no cap):
	// a real watermark is always ≥ 1 because the user message saves first.
	historyWatermark int
}

// maxLiveTurnEvents caps the replay buffer; a turn that somehow exceeds it
// (hundreds of tool rounds) drops its oldest events rather than growing
// without bound. The cap stays under the 256-slot conn send buffer so a full
// replay can never overflow a fresh connection.
const maxLiveTurnEvents = 200

// maxLiveStdoutLines caps how many lines of a running tool's live output
// replayLiveState keeps for a late-subscribing tab. The full output still
// reaches the model and lands in the eventual tool_result — this only bounds
// what a page refresh mid-command can catch up on.
const maxLiveStdoutLines = 200

// appendEvent adds an already-broadcast turn event to the replay buffer.
// Caller holds liveStateMu.
func (ls *sessionLiveState) appendEvent(ev map[string]any) {
	ls.events = append(ls.events, ev)
	if n := len(ls.events) - maxLiveTurnEvents; n > 0 {
		ls.events = ls.events[n:]
	}
}

// flushDeltas folds the accumulated streaming deltas into the replay buffer
// as one synthetic event each, preserving their position relative to the
// tool call that ended the round. Caller holds liveStateMu.
func (ls *sessionLiveState) flushDeltas(sessionID string) {
	if ls.thinkingBuf.Len() > 0 {
		ls.appendEvent(map[string]any{
			"type":       "thinking_delta",
			"session_id": sessionID,
			"text":       ls.thinkingBuf.String(),
		})
		ls.thinkingBuf.Reset()
	}
	if ls.textBuf.Len() > 0 {
		ls.appendEvent(map[string]any{
			"type":       "text_delta",
			"session_id": sessionID,
			"text":       ls.textBuf.String(),
		})
		ls.textBuf.Reset()
	}
}

// ─── WS handler methods on Server ──────────────────────────────────────────

// listSessionsBrief returns a brief session list for the initial WS handshake.
func (s *Server) listSessionsBrief() []wsSessionInfo {
	sessions, err := agent.ListSessions(50)
	if err != nil {
		return nil
	}
	out := make([]wsSessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		source := sess.Source
		if source == "" {
			source = "manual"
		}
		name := sess.DisplayTitle()
		// Same pending-title overlay as toSessionItem: a title broadcast
		// mid-turn isn't on disk yet, and this list feeds the sidebar.
		if pt := s.peekPendingTitle(sess.ID); pt != "" && agent.IsAutoNamePlaceholder(sess.Title) {
			name = pt
		}
		_, pm, re, sr, ctxUsage := s.sessionStatusFields(sess)
		out = append(out, wsSessionInfo{
			ID:              sess.ID,
			Name:            name,
			Status:          s.sessionStatus(sess.ID),
			CreatedAt:       sess.CreatedAt.UnixMilli(),
			Source:          source,
			Model:           sess.Model,
			TotalTurns:      sess.TurnCount(),
			WorkingDir:      s.sessionCwd(sess),
			PermissionMode:  pm,
			ReasoningEffort: re,
			ShowReasoning:   sr,
			ContextUsage:    ctxUsage,
			PendingQuestion: s.hasPendingQuestion(sess.ID),
		})
	}
	return out
}

// hasPendingQuestion reports whether a session has an outstanding
// ask_user_question awaiting an answer, so a freshly-loaded session list
// (initial connect / page refresh) shows the sidebar badge immediately
// instead of waiting for the next session_activity broadcast.
func (s *Server) hasPendingQuestion(sessionID string) bool {
	s.pendingPromptMu.Lock()
	defer s.pendingPromptMu.Unlock()
	_, ok := s.pendingQuestions[sessionID]
	return ok
}

// SetSubscribed records the active session subscription for a connection.
func (s *Server) SetSubscribed(conn *wsConn, sessionID string) {}

// sendContextUsage pushes a session_update carrying the context-window fill %
// to a freshly-subscribed tab. Without it, opening or reloading an existing
// conversation leaves the composer's "Context" bar stuck at 0% until the next
// turn runs (the session list reports 0 and nothing else repopulates it).
//
// When a turn has already run in this process the live Agent is cached, so its
// real last-input-token count is exact. For a resumed session (cold process,
// or one never run here) we fall back to a chars/4 estimate over the persisted
// transcript — approximate, but far better than a misleading 0%.
func (s *Server) sendContextUsage(sessionID string, conn *wsConn) {
	pct := 0
	usedTokens := 0
	s.sessionAgentsMu.Lock()
	a := s.sessionAgents[sessionID]
	s.sessionAgentsMu.Unlock()
	sess, _ := agent.LoadSession(sessionID)
	if a != nil {
		// Turn in flight: the live Agent has the exact current count.
		if used, window := a.ContextUsage(); window > 0 && used > 0 {
			pct = used * 100 / window
			usedTokens = used
		}
	}
	if pct == 0 && sess != nil && sess.LastContextTokens > 0 {
		// Idle/resumed session — or a turn whose first provider call hasn't
		// reported usage yet (live agent returns 0). Report the real count
		// persisted from its last turn (matches what the turn-end broadcast sent).
		if window := agent.ContextWindow(sess.Model); window > 0 {
			pct = sess.LastContextTokens * 100 / window
			usedTokens = sess.LastContextTokens
		}
	}
	if pct == 0 && sess != nil {
		// No real count anywhere (a session predating the field, one that never
		// completed a turn with a real count, or a count too small to reach 1% of
		// the window): fall back to a transcript estimate. It carries no exact
		// token count, so clear usedTokens — the UI shows a bare arrow.
		pct = estimateContextPct(sess)
		usedTokens = 0
	}
	if pct <= 0 {
		return
	}
	if pct > 100 {
		pct = 100
	}
	_, pm, re, _, _ := s.sessionStatusFields(sess)
	b, err := json.Marshal(map[string]any{
		"type":             "session_update",
		"session_id":       sessionID,
		"context_usage":    pct,
		"context_tokens":   usedTokens,
		"working_dir":      s.sessionCwdByID(sessionID),
		"permission_mode":  pm,
		"reasoning_effort": re,
	})
	if err == nil {
		conn.send <- b
	}
}

// estimateContextPct approximates how full the model's context window a
// persisted transcript occupies, using the same heuristic Agent.ContextUsage
// falls back to (agent.EstimateTokens). Used only when no live token count is
// available (no Agent has run in this process for the session yet).
func estimateContextPct(sess *agent.Session) int {
	window := agent.ContextWindow(sess.Model)
	if window <= 0 {
		return 0
	}
	return agent.EstimateTokens(sess.Messages) * 100 / window
}

// replayLiveState replays in-progress agent state (progress + stdout) to a
// newly-subscribing browser tab so it catches up with what it missed.
func (s *Server) replayLiveState(sessionID string, conn *wsConn) {
	// Bring the background-task badge up to date for this tab regardless of
	// whether a turn is in flight — background processes outlive turns. Always
	// sent (even with zero running) so switching sessions clears a stale badge.
	infos := tools.SessionBackgroundManager(sessionID).ListRunning()
	if b, err := json.Marshal(backgroundTasksUpdate(sessionID, infos, time.Now())); err == nil {
		conn.send <- b
	}

	// Replay still-running workflow runs the same way: the live panel is
	// built entirely from workflow_event pushes with no initial fetch, so a
	// tab that (re)subscribes after a run already started never sees it
	// otherwise — the started/progress events already went to whichever
	// connection was live at the time, and broadcast() is fire-and-forget
	// with no queueing. Finished runs need no replay: the panel already
	// removes them on "done", so if that event was missed there is nothing
	// left to reconstruct.
	for _, run := range tools.SessionWorkflowManager(sessionID).List() {
		if run.Status != "running" {
			continue
		}
		if b, err := json.Marshal(map[string]any{
			"type":        "workflow_event",
			"session_id":  sessionID,
			"run_id":      run.ID,
			"description": run.Description,
			"kind":        "started",
			"line":        "",
			"status":      run.Status,
		}); err == nil {
			conn.send <- b
		}
		for _, line := range run.Logs {
			if b, err := json.Marshal(map[string]any{
				"type":        "workflow_event",
				"session_id":  sessionID,
				"run_id":      run.ID,
				"description": run.Description,
				"kind":        "progress",
				"line":        line,
				"status":      run.Status,
			}); err == nil {
				conn.send <- b
			}
		}
	}

	// Sub-agents have the identical gap: SubAgentOnEvent (see
	// handlers_prepare_toolturn.go) also broadcasts directly to the hub with
	// no buffering, so a late-subscribing tab misses a still-running
	// sub-agent entirely. mkSpawner is nil: this must never create a
	// session's sub-agent manager just to discover it's empty — nil back
	// means no sub-agent has run in this session yet. Replay the retained
	// tool-level events (excluding the terminal "done") so the panel shows
	// the current tool trail, not just a coarse "started" stub. Use non-
	// blocking sends to avoid stalling the subscribe handler if the client is
	// a slow consumer.
	if sam := tools.SessionSubAgentManager(sessionID, nil); sam != nil {
		for _, sa := range sam.ListRunning() {
			for _, ev := range sa.Events {
				if ev.Kind == "done" {
					continue
				}
				if b, err := json.Marshal(map[string]any{
					"type":        "sub_agent_event",
					"session_id":  sessionID,
					"agent_id":    sa.ID,
					"description": ev.Description,
					"agent_type":  ev.AgentType,
					"kind":        ev.Kind,
					"tool_name":   ev.ToolName,
					"tool_input":  ev.ToolInput,
				}); err == nil {
					select {
					case conn.send <- b:
					default:
					}
				}
			}
		}
	}

	// Snapshot the in-progress turn under the read lock: the event buffer,
	// any unflushed streaming deltas, the current progress, and buffered
	// stdout. Marshaling happens inside the lock because the builders and the
	// events slice are mutated by handleEvent under the write lock; sends
	// happen after release.
	var replay [][]byte
	s.liveStateMu.RLock()
	if state, ok := s.liveStates[sessionID]; ok && state.progress != nil {
		// Transcript events broadcast before this tab subscribed. Without
		// this a page refresh mid-turn loses every tool card (and any
		// streamed text) until the turn persists at its end.
		for _, ev := range state.events {
			if b, err := json.Marshal(ev); err == nil {
				replay = append(replay, b)
			}
		}
		// The in-flight round's deltas, not yet folded into events.
		if state.thinkingBuf.Len() > 0 {
			if b, err := json.Marshal(map[string]any{
				"type":       "thinking_delta",
				"session_id": sessionID,
				"text":       state.thinkingBuf.String(),
			}); err == nil {
				replay = append(replay, b)
			}
		}
		if state.textBuf.Len() > 0 {
			if b, err := json.Marshal(map[string]any{
				"type":       "text_delta",
				"session_id": sessionID,
				"text":       state.textBuf.String(),
			}); err == nil {
				replay = append(replay, b)
			}
		}
		p := state.progress
		if b, err := json.Marshal(map[string]any{
			"type":          "progress",
			"session_id":    sessionID,
			"message":       p.Message,
			"progress_type": p.ProgressType,
			"phase":         "active",
			"status":        "start",
			"started_at":    p.StartedAt,
		}); err == nil {
			replay = append(replay, b)
		}
		if len(state.stdoutLines) > 0 {
			if b, err := json.Marshal(map[string]any{
				"type":       "tool_stdout",
				"session_id": sessionID,
				"tool_id":    state.stdoutToolID,
				"lines":      state.stdoutLines,
			}); err == nil {
				replay = append(replay, b)
			}
		}
	}
	s.liveStateMu.RUnlock()

	// Buffered transcript events go out before the pending prompt so the tab
	// rebuilds the transcript in broadcast order — the prompt was asked after
	// the tool calls that precede it. Non-blocking sends mirror the hub's
	// slow-consumer policy; a fresh connection's 256-slot buffer always fits
	// a full replay.
	for _, b := range replay {
		select {
		case conn.send <- b:
		default:
		}
	}

	// If the session has no live turn, the subscribing tab may still hold a
	// stale streaming/progress state (e.g. the user switched away while the
	// turn ran and missed its completion broadcast). Emit an explicit idle
	// update so the frontend resets its indicator.
	if len(replay) == 0 {
		// Re-check under the lock: another goroutine may have just started a
		// turn between the RUnlock above and this point. If it did, the replay
		// buffer would be non-empty and we would not reach here; guard anyway.
		s.liveStateMu.RLock()
		_, stillLive := s.liveStates[sessionID]
		s.liveStateMu.RUnlock()
		if !stillLive {
			sess, _ := agent.LoadSession(sessionID)
			_, pm, re, sr, _ := s.sessionStatusFields(sess)
			if b, err := json.Marshal(map[string]any{
				"type":             "session_update",
				"session_id":       sessionID,
				"status":           "idle",
				"working_dir":      s.sessionCwdByID(sessionID),
				"permission_mode":  pm,
				"reasoning_effort": re,
				"show_reasoning":   sr,
			}); err == nil {
				select {
				case conn.send <- b:
				default:
				}
			}
		}
	}

	// Replay an outstanding interactive prompt. Its original broadcast only
	// reached the tabs connected at the time; without this, a page refresh
	// during ask_user_question / a permission confirmation leaves the new tab
	// stuck on a spinner with no way to answer.
	s.pendingPromptMu.Lock()
	pendingQ, hasQ := s.pendingQuestions[sessionID]
	pendingC, hasC := s.pendingConfirms[sessionID]
	s.pendingPromptMu.Unlock()
	if hasQ {
		if b, err := json.Marshal(pendingQ); err == nil {
			conn.send <- b
		}
	}
	if hasC {
		if b, err := json.Marshal(pendingC); err == nil {
			conn.send <- b
		}
	}
}

// handleWSUserMessage processes a user message from the WebSocket.
// When a turn is already running the message is enqueued as steer and surfaced
// to the frontend as a pending ghost; the turn loop consumes it automatically.
func (s *Server) handleWSUserMessage(conn *wsConn, msg *wsMsgUserMessage) {
	sid := msg.SessionID
	if sid == "" {
		return
	}

	content := extractTextContent(msg.Content)
	// conn is nil for server-injected messages (e.g. a mid-turn steer replayed
	// through this handler): treat those as non-loopback so a real path is only
	// ever honored for a message that genuinely arrived from a local peer.
	loopback := conn != nil && conn.loopback

	sess, err := agent.LoadSession(sid)
	if err != nil {
		s.wsHub.broadcast(sid, map[string]any{
			"type":       "send_rejected",
			"session_id": sid,
			"message":    fmt.Sprintf("session not found: %s", sid),
		})
		return
	}

	// Gate image attachments on the active model's vision capability so a
	// text-only model isn't sent image blocks it rejects (HTTP 400). LoadCached
	// keeps the last good vision setting even if config.yml is mid-edit.
	cfg, _ := config.LoadCached()
	vision := cfg.ModelVision(sess.Model)

	att := parseUserFiles(msg.Files, loopback, vision)
	if content == "" && len(att.blocks) == 0 && len(att.notes) == 0 {
		return
	}

	// Note: a user message does NOT cancel an armed loop — the loop coexists
	// with the conversation (CC-style). It stops only on an explicit interrupt
	// or schedule_wakeup(cancel=true).

	// Session-management slash commands handled inline (no model turn). Other
	// "/..." text falls through to the model, matching the TUI where unknown
	// slashes are ordinary input.
	if len(att.blocks) == 0 && len(att.notes) == 0 {
		trimmed := strings.TrimSpace(content)
		switch strings.ToLower(trimmed) {
		case "/clear":
			s.wsClearSession(sid)
			return
		case "/compact":
			s.wsCompactSession(sid)
			return
		}
		if trimmed == "/goal" || strings.HasPrefix(trimmed, "/goal ") {
			s.wsGoalCommand(sid, strings.TrimSpace(strings.TrimPrefix(trimmed, "/goal")))
			return
		}
	}
	// Document attachments ride as path notes in the text so the model can
	// read_file them and the transcript keeps a visible record.
	if len(att.notes) > 0 {
		content = strings.TrimSpace(content + "\n\n" + strings.Join(att.notes, "\n"))
	}

	if ok, bindMsg, berr := s.acquireSessionBinding(sid, agent.EntryWeb, msg.Force); !ok {
		// A binding held by another entry without an active lease is recoverable:
		// ask the user to confirm a takeover instead of silently dropping the message.
		if s.canForceBind(sid, berr) {
			s.wsHub.broadcast(sid, map[string]any{
				"type":       "bind_required",
				"session_id": sid,
				"message":    berr.Error(),
			})
			return
		}
		s.wsHub.broadcast(sid, map[string]any{
			"type":       "send_rejected",
			"session_id": sid,
			"message":    berr.Error(),
		})
		return
	} else if bindMsg != "" {
		s.wsToast(sid, bindMsg, "info")
	}

	mu := s.sessionTurnLock(sid)
	mu.Lock()

	if s.turnRunning[sid] {
		mu.Unlock()
		// The current Web entry already owns the binding; a mid-turn message
		// has exactly one home: the running Agent's Inbox when it is registered
		// (the runLoop drains it between iterations — attachment blocks and
		// all), the steer queue otherwise (consumed by runAgentTurnLoop as the
		// next chained turn). Enqueueing into both, as this branch once did,
		// processed the same message twice.
		s.sessionAgentsMu.Lock()
		a := s.sessionAgents[sid]
		if a != nil {
			a.Inbox.EnqueueWithBlocks(content, att.blocks)
		}
		s.sessionAgentsMu.Unlock()
		if a == nil {
			s.enqueueSteer(sid, agent.InboxItem{Text: content, Blocks: att.blocks})
		}
		// The frontend already rendered a ghost bubble in _sendMessage;
		// history_user_message (broadcast when the turn drains steer) will
		// replace it.  No need for a separate pending_user_messages event.
		return
	}

	// Reload the authoritative session after acquiring the binding so the turn
	// works on the latest persisted state (another process may have saved).
	sess, err = agent.LoadSession(sid)
	if err != nil {
		mu.Unlock()
		s.releaseSessionBinding(sid, agent.EntryWeb)
		s.wsHub.broadcast(sid, map[string]string{
			"type":    "error",
			"message": fmt.Sprintf("session not found: %s", sid),
		})
		return
	}

	sess.IncFlight()
	s.turnRunning[sid] = true
	mu.Unlock()

	go func() {
		defer func() {
			// Release the binding — which reloads the session and appends a
			// lease-clear record, writing the session file — BEFORE clearing
			// turnRunning. That flag is the "turn fully wound down" barrier
			// (tests and drain logic wait on it); flipping it while a session
			// write is still pending lets t.TempDir() cleanup race the open
			// handle on Windows ("directory is not empty").
			sess.DecFlight()
			s.releaseSessionBinding(sid, agent.EntryWeb)
			mu.Lock()
			s.turnRunning[sid] = false
			mu.Unlock()
		}()
		s.runAgentTurnLoop(sess, content, att.blocks, att.images)
	}()
}

// wsClearSession wipes a session's persisted history (keeping its meta header)
// and tells subscribed browsers to reload the now-empty transcript. Backs the
// /clear command. Refused while a turn is running so it can't race the turn's
// own post-turn persist.
func (s *Server) wsClearSession(sid string) {
	mu := s.sessionTurnLock(sid)
	mu.Lock()
	running := s.turnRunning[sid]
	mu.Unlock()
	if running {
		s.wsToast(sid, "Can't clear while a turn is running — interrupt it first.", "error")
		return
	}

	sess, err := agent.LoadSession(sid)
	if err != nil {
		s.wsToast(sid, "Session not found.", "error")
		return
	}
	sess.Messages = nil
	if err := sess.Save(); err != nil {
		s.wsToast(sid, "Clear failed: "+err.Error(), "error")
		return
	}

	// Drop the cached agent and the memory-recall latch so the next turn starts
	// from a genuinely fresh context (delete on a nil map is a no-op).
	s.sessionAgentsMu.Lock()
	delete(s.sessionAgents, sid)
	s.sessionAgentsMu.Unlock()
	s.injectorMu.Lock()
	delete(s.sessionInjectors, sid)
	s.injectorMu.Unlock()

	s.wsHub.broadcast(sid, map[string]any{"type": "session_update", "session_id": sid, "status": "idle", "context_usage": 0, "context_tokens": 0})
	s.broadcastHistoryReload(sid)
	s.wsToast(sid, "Conversation cleared.", "success")
}

// wsCompactSession force-compacts a session's history now and reloads the
// transcript. Backs the /compact command. The summarize is an LLM call, so it
// runs in a goroutine (the WS read loop must not block) guarded by turnRunning
// so a model turn can't race it; it registers an interrupt so /stop works.
func (s *Server) wsCompactSession(sid string) {
	mu := s.sessionTurnLock(sid)
	mu.Lock()
	if s.turnRunning[sid] {
		mu.Unlock()
		s.wsToast(sid, "Can't compact while a turn is running — interrupt it first.", "error")
		return
	}
	s.turnRunning[sid] = true
	mu.Unlock()

	go func() {
		defer s.recoverBg("web compaction")
		defer func() {
			mu.Lock()
			s.turnRunning[sid] = false
			mu.Unlock()
		}()

		sess, err := agent.LoadSession(sid)
		if err != nil {
			s.wsToast(sid, "Session not found.", "error")
			return
		}
		if err := s.ensureSender(); err != nil {
			s.wsToast(sid, err.Error(), "error")
			return
		}
		a := s.buildAgent(sess)

		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKeySessionID{}, sid))
		s.registerInterrupt(sid, cancel)
		defer func() {
			cancel()
			s.interruptMu.Lock()
			delete(s.interrupts, sid)
			s.interruptMu.Unlock()
		}()

		s.wsHub.broadcast(sid, map[string]any{"type": "session_update", "session_id": sid, "status": "running"})
		defer s.wsHub.broadcast(sid, map[string]any{"type": "session_update", "session_id": sid, "status": "idle"})

		stats, err := a.ForceCompact(ctx, nil)
		if err != nil {
			s.wsToast(sid, "Compact failed: "+err.Error(), "error")
			s.broadcastHistoryReload(sid)
			return
		}
		if stats.FoldedMsgs == 0 && stats.ReclaimedTokens == 0 {
			s.wsToast(sid, "Nothing to compact yet.", "info")
			s.broadcastHistoryReload(sid)
			return
		}
		sess.SyncFrom(a.History)
		if err := sess.Save(); err != nil {
			s.wsToast(sid, "Compact failed to save: "+err.Error(), "error")
			s.broadcastHistoryReload(sid)
			return
		}
		s.broadcastHistoryReload(sid)
		s.wsToast(sid, fmt.Sprintf("Compacted — folded %d message(s).", stats.FoldedMsgs), "success")
	}()
}

// broadcastHistoryReload asks subscribed browsers to re-fetch the transcript,
// used after an out-of-band history rewrite (/clear, /compact).
func (s *Server) broadcastHistoryReload(sid string) {
	s.wsHub.broadcast(sid, map[string]string{"type": "history_reload", "session_id": sid})
}

// wsToast surfaces a transient message in the browser (level: success | info |
// error).
func (s *Server) wsToast(sid, message, level string) {
	s.wsHub.broadcast(sid, map[string]string{
		"type":       "toast",
		"session_id": sid,
		"message":    message,
		"level":      level,
	})
}

// handleWSInterrupt sends an interrupt signal for a session and broadcasts
// the interrupted event so the frontend shows the cancellation to the user.
func (s *Server) handleWSInterrupt(sessionID string) {
	s.interruptSession(sessionID)
	s.wsHub.broadcast(sessionID, map[string]any{
		"type":       "interrupted",
		"session_id": sessionID,
	})
}

// interruptSession cancels a session's in-flight turn, if any, including a
// goroutine parked in ask_user_question — deleting a session must go through
// this first, otherwise a blocked wsAsker.Ask (which no longer has a timeout
// to fall back on) leaks forever and, if answered via a stale modal, resaves
// the session file the delete just removed. Also stops any armed loop
// wakeup (TUI / CC parity). Does not broadcast; callers that want the
// frontend "interrupted" toast (handleWSInterrupt) add it themselves.
func (s *Server) interruptSession(sessionID string) {
	s.cancelWakeup(sessionID)
	s.interruptMu.Lock()
	if cancel, ok := s.interrupts[sessionID]; ok {
		cancel()
		delete(s.interrupts, sessionID)
	}
	s.interruptMu.Unlock()
}

// lastVisibleUserIdx returns the index of the most recent user message that
// is a real prompt: not a tool_result carrier, with user-visible text (after
// stripping <system-reminder> spans) or an image attachment. -1 if none.
// Backwards-scanning for RoleUser alone would land on the tool_result carrier
// of an agentic turn and "retry" an empty message.
func lastVisibleUserIdx(msgs []agent.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != agent.RoleUser {
			continue
		}
		carrier, hasImage := false, false
		text := m.Content
		for _, b := range m.Blocks {
			switch b.Type {
			case "tool_result":
				carrier = true
			case "image":
				hasImage = true
			case "text":
				if text == "" {
					text = b.Text
				}
			}
		}
		if carrier {
			continue
		}
		if hasImage || strings.TrimSpace(agent.StripSystemReminders(text)) != "" {
			return i
		}
	}
	return -1
}

// broadcastRollback tells subscribed browsers the transcript tail was
// stripped, so they re-render from the API before any new events stream in.
func (s *Server) broadcastRollback(sessionID string) {
	s.wsHub.broadcast(sessionID, map[string]string{
		"type":       "history_rollback",
		"session_id": sessionID,
	})
}

// handleWSRetry re-runs the last turn by stripping the last assistant reply
// from the session and resending the last user message.
func (s *Server) handleWSRetry(conn *wsConn, sessionID string) {
	sess, err := agent.LoadSession(sessionID)
	if err != nil {
		s.wsHub.broadcast(sessionID, map[string]string{
			"type":    "error",
			"message": "session not found",
		})
		return
	}

	// Find the last real user prompt and strip everything from it on.
	lastUserIdx := lastVisibleUserIdx(sess.Messages)
	if lastUserIdx < 0 {
		s.wsHub.broadcast(sessionID, map[string]string{
			"type":    "error",
			"message": "no user message to retry",
		})
		return
	}

	mu := s.sessionTurnLock(sess.ID)
	mu.Lock()

	if s.turnRunning[sess.ID] {
		mu.Unlock()
		s.wsHub.broadcast(sessionID, map[string]string{
			"type":    "error",
			"message": "Cannot retry while a turn is running. Please interrupt first.",
		})
		return
	}

	userMsg := sess.Messages[lastUserIdx]
	sess.Messages = sess.Messages[:lastUserIdx]
	_ = sess.Save()
	s.broadcastRollback(sessionID)

	// A multipart user message (image attachments) keeps its text in a text
	// block; re-attach the image blocks (rehydrated by LoadSession) so the
	// retried turn re-sends them.
	content := userMsg.Content
	var blocks []agent.ContentBlock
	for _, b := range userMsg.Blocks {
		switch b.Type {
		case "text":
			if content == "" {
				content = b.Text
			}
		case "image":
			blocks = append(blocks, b)
		}
	}
	images := imageRefsFromBlocks(blocks)

	s.turnRunning[sess.ID] = true
	mu.Unlock()

	go func() {
		defer func() {
			mu.Lock()
			s.turnRunning[sess.ID] = false
			mu.Unlock()
		}()
		s.runAgentTurnLoop(sess, content, blocks, images)
	}()
}

// handleWSRollback strips the last turn — the last real user prompt and
// everything after it — without re-running. This is the edit-and-resend flow:
// the browser pulls the original text into the composer before sending
// rollback, the transcript re-renders without the turn, and the edited
// message arrives as a fresh user_message.
func (s *Server) handleWSRollback(conn *wsConn, sessionID string) {
	sess, err := agent.LoadSession(sessionID)
	if err != nil {
		s.wsHub.broadcast(sessionID, map[string]string{
			"type":    "error",
			"message": "session not found",
		})
		return
	}

	lastUserIdx := lastVisibleUserIdx(sess.Messages)
	if lastUserIdx < 0 {
		s.wsHub.broadcast(sessionID, map[string]string{
			"type":    "error",
			"message": "no user message to roll back",
		})
		return
	}

	mu := s.sessionTurnLock(sess.ID)
	mu.Lock()
	defer mu.Unlock()

	if s.turnRunning[sess.ID] {
		s.wsHub.broadcast(sessionID, map[string]string{
			"type":    "error",
			"message": "Cannot edit while a turn is running. Please interrupt first.",
		})
		return
	}

	sess.Messages = sess.Messages[:lastUserIdx]
	_ = sess.Save()
	s.broadcastRollback(sessionID)
}

// handleWSRunTask triggers a scheduled task run immediately from the Web UI.
func (s *Server) handleWSRunTask(conn *wsConn, sessionID string) {
	s.initScheduler()
	if s.scheduler == nil {
		s.wsHub.broadcast(sessionID, map[string]string{
			"type":    "error",
			"message": "scheduler not available",
		})
		return
	}
	newSessionID, err := s.scheduler.RunNow(sessionID)
	if err != nil {
		s.wsHub.broadcast(sessionID, map[string]string{
			"type":    "error",
			"message": err.Error(),
		})
		return
	}
	s.wsHub.broadcast(sessionID, map[string]string{
		"type":       "task_started",
		"session_id": newSessionID,
	})
}

// handleWSConfirmation delivers a confirmation answer from one client and
// broadcasts a completion event so every other subscribed client can close
// its modal for the same confirmation.
func (s *Server) handleWSConfirmation(confID, result string) {
	s.confirmMu.Lock()
	if ch, ok := s.confirmations[confID]; ok {
		ch <- result
	}
	s.confirmMu.Unlock()

	s.pendingPromptMu.Lock()
	defer s.pendingPromptMu.Unlock()
	for sessionID, pending := range s.pendingConfirms {
		if pending.ConfID == confID {
			s.wsHub.broadcast(sessionID, wsEventConfirmationComplete{
				Type:      "confirmation_complete",
				SessionID: sessionID,
				ConfID:    confID,
				Result:    result,
			})
			return
		}
	}
}

// extractTextContent extracts plain text from content which may be a string
// or a multipart array (e.g. [{type:"text", text:"..."}, {type:"image_url", ...}]).
func extractTextContent(raw json.RawMessage) string {
	// Try string first.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	// Try multipart array.
	var parts []map[string]any
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if t, ok := p["type"].(string); ok && t == "text" {
			if txt, ok := p["text"].(string); ok {
				texts = append(texts, txt)
			}
		}
	}
	return joinNonEmpty(texts, "\n")
}

func joinNonEmpty(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			result += sep + parts[i]
		}
	}
	return result
}

// recoverTurn recovers a panic from an interactive turn so one bad turn fails
// gracefully instead of crashing the whole serve process. Turns run in bare
// goroutines (outside net/http's per-request recover), and because the desktop
// app runs the server in-process, an unrecovered panic here would terminate the
// process — taking every session and the app itself down, leaving the window
// frozen. It logs the panic with a stack and pushes the end-of-turn frames the
// panicking turn skipped, so the composer leaves its "thinking" state and shows
// an error rather than spinning forever.
func (s *Server) recoverTurn(sessionID string) {
	r := recover()
	if r == nil {
		return
	}
	slog.Error("recovered panic in agent turn", "session_id", sessionID, "panic", r, "stack", string(debug.Stack()))
	if s.wsHub == nil {
		return
	}
	s.wsHub.broadcast(sessionID, map[string]any{
		"type":       "error",
		"session_id": sessionID,
		"message":    "The turn stopped unexpectedly (internal error). Your session is intact — send another message to continue.",
	})
	// Mirror the normal end-of-turn tail so the UI leaves its "thinking" state.
	s.wsHub.broadcast(sessionID, map[string]any{"type": "complete", "session_id": sessionID})
	s.wsHub.broadcast(sessionID, map[string]any{"type": "session_update", "session_id": sessionID, "status": "idle"})
}

// recoverBg recovers a panic from a background/throwaway goroutine (title and
// suggestion generation, compaction, scheduled tasks). Same rationale as
// recoverTurn — a panic in any goroutine crashes the process — but there is no
// interactive turn to unstick, so it only logs.
func (s *Server) recoverBg(what string) {
	if r := recover(); r != nil {
		slog.Error("recovered panic in background goroutine", "what", what, "panic", r, "stack", string(debug.Stack()))
	}
}

// ─── runAgentTurnLoop / doAgentTurn ────────────────────────────────────────
//
// runAgentTurnLoop consumes steer messages queued mid-turn and chains turns
// until the queue is empty.  This mirrors the TUI's behaviour where inbox
// messages drained after a turn are automatically fed into the next one.
//
// doAgentTurn is the single-turn body shared by the loop and retry paths.

func (s *Server) runAgentTurnLoop(sess *agent.Session, initialContent string, blocks []agent.ContentBlock, images []string) {
	defer s.recoverTurn(sess.ID)
	if err := s.drain.begin(); err != nil {
		// Restart drain in progress: surface a retryable error to the
		// browser instead of starting a turn the shutdown would cut short.
		if s.wsHub != nil {
			s.wsHub.broadcast(sess.ID, map[string]string{
				"type":       "error",
				"message":    err.Error(),
				"session_id": sess.ID,
			})
		}
		return
	}
	defer s.drain.end()

	content := initialContent
	for {
		s.doAgentTurn(sess, content, blocks, images)
		if s.drain.isDraining() {
			// Don't chain queued steer messages into fresh turns during a
			// drain — they'd start work the shutdown cuts at the timeout.
			// Tell the user to resend instead of eating the input silently.
			if items := s.drainSteer(sess.ID); len(items) > 0 && s.wsHub != nil {
				s.wsHub.broadcast(sess.ID, map[string]string{
					"type":       "error",
					"message":    errDraining.Error(),
					"session_id": sess.ID,
				})
			}
			break
		}
		// An active goal continues unprompted: enqueue the hidden continuation
		// prompt so the chain below starts the follow-up turn. User steers
		// queued meanwhile take priority — GoalContinuation is only consulted
		// when the queue is empty, and its own guards (status, zero-progress
		// suppression) decide whether the loop keeps going.
		if s.goalsEnabled.Load() && !s.steerPending(sess.ID) {
			if prompt, ok := sess.GoalContinuation(); ok {
				s.enqueueSteer(sess.ID, agent.InboxItem{Text: prompt})
			}
		}
		steerItems := s.drainSteer(sess.ID)
		if len(steerItems) == 0 {
			break
		}
		// Fold the queued steer items into one chained turn: texts joined,
		// attachment blocks carried over, thumbnails re-derived.
		var texts []string
		blocks = nil
		for _, it := range steerItems {
			if strings.TrimSpace(it.Text) != "" {
				texts = append(texts, it.Text)
			}
			blocks = append(blocks, it.Blocks...)
		}
		content = strings.Join(texts, "\n\n")
		images = imageRefsFromBlocks(blocks)
	}
}

func (s *Server) enqueueSteer(sessionID string, items ...agent.InboxItem) {
	s.steerMu.Lock()
	s.steerQueues[sessionID] = append(s.steerQueues[sessionID], items...)
	s.steerMu.Unlock()
}

func (s *Server) drainSteer(sessionID string) []agent.InboxItem {
	s.steerMu.Lock()
	items := s.steerQueues[sessionID]
	s.steerQueues[sessionID] = nil
	s.steerMu.Unlock()
	return items
}

// removeSteerFromQueue deletes the last queued steer item whose text matches,
// mirroring Inbox.Remove for the queue path (used when no live Agent is
// registered). Reports whether one was removed.
func (s *Server) removeSteerFromQueue(sessionID, text string) bool {
	s.steerMu.Lock()
	defer s.steerMu.Unlock()
	q := s.steerQueues[sessionID]
	for i := len(q) - 1; i >= 0; i-- {
		if q[i].Text == text {
			s.steerQueues[sessionID] = append(q[:i], q[i+1:]...)
			return true
		}
	}
	return false
}

// handleWSRetractSteer pulls a not-yet-consumed steer message back out of the
// running turn's inbox (or the chained-turn steer queue) so the web UI can drop
// its ghost bubble and reload the text into the composer — the web counterpart
// of the TUI's ↑ recall. A steer already drained by the loop can't be retracted
// (it's committed to the turn); the failure is reported so the UI keeps the
// bubble instead of stranding its text.
func (s *Server) handleWSRetractSteer(sessionID, pendingID, text string) {
	removed := false
	s.sessionAgentsMu.Lock()
	a := s.sessionAgents[sessionID]
	s.sessionAgentsMu.Unlock()
	if a != nil {
		removed = a.Inbox.Remove(text)
	}
	if !removed {
		removed = s.removeSteerFromQueue(sessionID, text)
	}
	if s.wsHub == nil {
		return
	}
	evType := "steer_retract_failed"
	if removed {
		evType = "steer_retracted"
	}
	s.wsHub.broadcast(sessionID, map[string]any{
		"type":       evType,
		"session_id": sessionID,
		"pending_id": pendingID,
	})
}

// crashRecoveryReminder is prepended (as model-facing context, stripped from
// every UI surface by StripSystemReminders) to the first user message of a
// turn whose session transcript still ends mid-turn — meaning the previous
// turn died with the server process. Its rounds were persisted incrementally,
// but the round in flight at the crash is gone, so executed tools may have
// changed state without their results being recorded.
const crashRecoveryReminder = `<system-reminder>The previous turn in this session ended abnormally (the server stopped mid-turn). Tool calls from that turn may have executed and changed state even if their results are missing from this conversation. Verify the current state before repeating or continuing potentially destructive actions.</system-reminder>`

func (s *Server) doAgentTurn(sess *agent.Session, content string, blocks []agent.ContentBlock, images []string) {
	// A transcript that still ends mid-turn here means the previous turn died
	// with the server — a finished or user-interrupted turn always ends on a
	// plain assistant message. Warn the model once: the reminder rides this
	// turn's user message, and the turn's own completion makes the tail clean
	// again, so it cannot re-fire.
	if sess.EndsMidTurn() {
		content = crashRecoveryReminder + "\n\n" + content
	}

	// Build the user message first: its CreatedAt is both the persisted
	// timestamp and the broadcast created_at below, so the live event and a
	// concurrent history fetch carry the SAME dedup key. (Mirror
	// appendUserInput's multipart shape — optional text block, then
	// attachments — so the persisted line matches what RunStream appends.)
	userMsg := agent.NewUserMessage(content)
	if len(blocks) > 0 {
		multi := make([]agent.ContentBlock, 0, len(blocks)+1)
		if content != "" {
			multi = append(multi, agent.NewTextBlock(content))
		}
		userMsg.Content = ""
		userMsg.Blocks = append(multi, blocks...)
	}

	// Confirm the user message immediately so the frontend can swap the
	// ghost (.msg-pending) bubble for the real one before streaming starts.
	// <system-reminder> spans are stripped from the bubble: a turn kicked by a
	// completion note (kickIdleSteerTurn) is pure reminder and renders nothing.
	// Document attachments show as chips derived from their "[Attached file: …]"
	// notes: strip the notes from the displayed text (they stay in the persisted,
	// model-facing content) and add a chip ref per note. Deriving here rather
	// than trusting a pre-filled images slice keeps every turn path — web, retry,
	// background — consistent and stops a note-only message from vanishing.
	visible, docRefs := docChipRefs(strings.TrimSpace(agent.StripSystemReminders(content)))
	images = append(images, docRefs...)
	if visible != "" || len(images) > 0 {
		userEvent := map[string]any{
			"type":          "history_user_message",
			"session_id":    sess.ID,
			"content":       visible,
			"created_at":    userMsg.CreatedAt.UnixMilli(),
			"message_index": len(sess.Messages), // position in the persisted Messages array (before this message is appended)
		}
		if len(images) > 0 {
			userEvent["images"] = images
		}
		s.wsHub.broadcast(sess.ID, userEvent)
	}

	// Persist the user message right away so a page refresh mid-turn doesn't
	// lose it.  We append it for Save(), then pop it back off so buildAgent
	// doesn't double-count it — RunStream will add the same message to
	// a.History via appendUserInput. The count including the user message is
	// the turn's history watermark: while the turn runs, the history endpoint
	// serves only messages below it and the WS replay buffer owns the rest.
	sess.Messages = append(sess.Messages, userMsg)
	_ = sess.Save()
	historyWatermark := len(sess.Messages)
	sess.Messages = sess.Messages[:len(sess.Messages)-1]

	sw := s.newWSStreamWriter(sess.ID)

	if err := s.ensureSender(); err != nil {
		sw.error(err.Error())
		return
	}

	// Set up progress tracking for this session, seeded with an initial
	// "thinking" phase that is broadcast immediately. Building the agent,
	// connecting to the provider, and the model's pre-text reasoning can take
	// several seconds during which no tool or text event fires. Without this
	// seed the frontend swaps the ghost bubble for the real one and then sits
	// with no indicator until the first delta — the session looks hung. The
	// real tool/text events adopt this progress element in place (the frontend
	// keys on started_at), so there is no flicker, and a late-subscribing tab
	// replays it via replayLiveState.
	startedAt := time.Now().UnixMilli()
	s.liveStateMu.Lock()
	s.liveStates[sess.ID] = &sessionLiveState{
		progress: &wsEventProgress{
			Type:         "progress",
			ProgressType: "thinking",
			Phase:        "active",
			StartedAt:    startedAt,
		},
		historyWatermark: historyWatermark,
	}
	s.liveStateMu.Unlock()
	s.wsHub.broadcast(sess.ID, map[string]any{
		"type":          "progress",
		"session_id":    sess.ID,
		"progress_type": "thinking",
		"phase":         "active",
		"status":        "start",
		"started_at":    startedAt,
	})

	defer func() {
		// Clear live state at the end of the turn.
		s.liveStateMu.Lock()
		delete(s.liveStates, sess.ID)
		s.liveStateMu.Unlock()
	}()

	runCtx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKeySessionID{}, sess.ID))
	// Stamp the per-session Waker so schedule_wakeup (the in-session loop) can pace
	// this and later turns — including the wakeup-injected turns kicked via
	// kickIdleSteerTurn, which also flow through here.
	runCtx = tools.WithWaker(runCtx, s.wakerFor(sess.ID))
	s.registerInterrupt(sess.ID, cancel)
	defer func() {
		cancel()
		s.interruptMu.Lock()
		delete(s.interrupts, sess.ID)
		s.interruptMu.Unlock()
	}()

	// Tell the frontend the turn started: "running" is what shows the
	// interrupt button (the turn-end paths broadcast "idle" to hide it).
	s.wsHub.broadcast(sess.ID, map[string]any{
		"type":       "session_update",
		"session_id": sess.ID,
		"status":     "running",
	})

	a := s.buildAgent(sess)
	if len(blocks) > 0 {
		// Image attachments fold into the same user turn as the text when
		// RunStream appends the user input.
		a.AttachUserBlocks(blocks)
	}
	// The persisted user message RunStream appends must carry the SAME created_at
	// we broadcast live above, or the frontend double-renders it. Hand the
	// pre-stamped timestamp to the turn rather than letting appendUserInput mint
	// a second, later one.
	a.AttachUserCreatedAt(userMsg.CreatedAt)

	// Flush any steer messages that arrived before the Agent was built into
	// the Agent's Inbox so the runLoop can drain them between iterations.
	for _, it := range s.drainSteer(sess.ID) {
		a.Inbox.EnqueueWithBlocks(it.Text, it.Blocks)
	}

	// Register this Agent so concurrent mid-turn messages can reach its Inbox.
	s.sessionAgentsMu.Lock()
	s.sessionAgents[sess.ID] = a
	if s.liveSessions == nil { // tests build minimal Servers by hand
		s.liveSessions = make(map[string]*agent.Session)
	}
	s.liveSessions[sess.ID] = sess
	s.sessionAgentsMu.Unlock()
	// Teardown: drain any messages still in the Inbox (they arrived during the
	// final LLM round, or the turn errored before draining them) back to the
	// steer queue, then deregister — BOTH under sessionAgentsMu so a concurrent
	// deliverModelNote (background/sub-agent exit hook) either enqueues before
	// the drain (caught here) or, if it runs after, sees the agent already gone
	// and routes to the steer queue itself. Splitting these into two locked
	// sections left a gap where a note enqueued between the drain and the
	// deregister was silently lost.
	defer func() {
		s.sessionAgentsMu.Lock()
		if items := a.Inbox.Drain(); len(items) > 0 {
			s.enqueueSteer(sess.ID, items...)
		}
		delete(s.sessionAgents, sess.ID)
		delete(s.liveSessions, sess.ID)
		s.sessionAgentsMu.Unlock()
	}()

	var toolDefs []agent.ToolDefinition
	var executor agent.ToolExecutor
	if s.cfg.Tools {
		var perr error
		var cleanup func()
		defer func() {
			if cleanup != nil {
				cleanup()
			}
		}()
		// prepareToolTurn wires the session-scoped sub-agent manager's hooks
		// (live-panel events + completion notes to the model).
		runCtx, executor, _, cleanup, perr = s.prepareToolTurn(runCtx, a, sess)
		if perr != nil {
			sw.error(perr.Error())
			return
		}
		toolDefs = tools.DefaultToolsForCtx(runCtx, a.Model)
		// Surface background-process completions (badge + chat notice).
		s.wireBackgroundTaskNotices(sess.ID)
	}

	// Once per session, as soon as its first user message arrives: generate a
	// sidebar title. Fired on receipt rather than at turn end so the user isn't
	// staring at the placeholder for the whole first turn (an agentic turn can
	// run minutes). The throwaway call runs concurrently with the turn's own
	// provider call, off a pre-turn copy of sess.Messages plus this turn's user
	// message — the live History belongs to the loop goroutine (and is still
	// empty here).
	//
	// The goroutine does NOT write the session file: the turn is concurrently
	// rewriting it (persistTurnProgress on every event, plus a compaction
	// rewrite), and a second writer here — via its own LoadSession'd Session —
	// could truncate the file to a stale, shorter message list and lose the
	// turn's transcript. Instead it broadcasts the rename (live UX) and hands
	// the title to the turn goroutine via storePendingTitle; the turn adopts
	// it after its final write, on the single serialized write path. If the
	// title lands after that point it rides the next turn's adoption.
	//
	// One mechanism, one storage: GenerateTitleOrSnippet is the same call the
	// TUI makes — an LLM title within TitleGenerationTimeout, else a snippet
	// of the first user message — and the turn-end SetTitle adoption is the
	// only persistence path. A provider failure is logged and the snippet
	// still applies; the claim releases either way and the next user message
	// retries.
	if agent.IsAutoNamePlaceholder(sess.Title) {
		sid := sess.ID
		titleMsgs := append(append([]agent.Message{}, sess.Messages...), userMsg)
		// No user text to title from at all (e.g. an attachments-only first
		// message): skip the throwaway call rather than pay for a hallucinated
		// title — the snippet fallback would come up empty anyway. The next
		// text-bearing user message retries.
		if agent.FirstUserSnippet(titleMsgs) != "" && s.claimTitleGeneration(sid) {
			go func() {
				defer s.recoverBg("title generation")
				defer s.releaseTitleGeneration(sid)
				ctx, cancel := context.WithTimeout(context.Background(), agent.TitleGenerationTimeout)
				defer cancel()
				t, terr := a.GenerateTitleOrSnippet(ctx, titleMsgs)
				if terr != nil {
					slog.Warn("session title generation failed, falling back to message snippet", "session_id", sid, "err", terr)
				}
				if strings.TrimSpace(t) == "" {
					return
				}
				// Hand the title to the turn goroutine (it persists it) and
				// broadcast the rename so every tab's sidebar updates live.
				s.storePendingTitle(sid, t)
				s.wsHub.broadcast("", map[string]any{
					"type":       "session_renamed",
					"session_id": sid,
					"name":       t,
				})
			}()
		}
	}

	// Persist the turn's progress incrementally: after any event that grew or
	// rewrote history, flush it to disk so a server crash mid-turn loses at
	// most the round in flight, not the whole turn. The length/dirty gate
	// makes the per-delta calls free — Save itself is also a no-op when
	// nothing changed. RunStream invokes the handler synchronously on this
	// goroutine, so sess needs no extra locking.
	lastSavedLen := -1
	persistTurnProgress := func() {
		if n := a.History.Len(); n != lastSavedLen || a.History.RewriteDirty() {
			sess.SyncFrom(a.History)
			if sess.Save() == nil {
				lastSavedLen = n
			}
		}
	}
	handler := func(ev agent.AgentEvent) {
		sw.handleEvent(ev)
		persistTurnProgress()
	}

	turnCallStart := time.Now()
	reply, err := a.RunStream(runCtx, content, toolDefs, executor, handler)
	// Whether this turn was goal-continuation-kicked, read before the error
	// handling below consumes the pending mark via SuppressGoalContinuation.
	goalContWasPending := s.goalsEnabled.Load() && sess.GoalContinuationPending()

	// Save history even on interrupt — finishInterrupted repairs it so the
	// session stays well-formed for the next turn.
	sess.SyncFrom(a.History)
	_ = sess.Save()

	// The turn is persisted: drop the live state (and its replay buffer) now,
	// before any further broadcasts, so a tab subscribing from here on
	// rebuilds from history alone instead of also replaying buffered events
	// on top of it. (This is the primary cleanup point — the EventTurnDone
	// handler deliberately leaves the state alone; the deferred delete stays
	// as a backstop for panics.)
	s.liveStateMu.Lock()
	delete(s.liveStates, sess.ID)
	s.liveStateMu.Unlock()

	if err != nil {
		// Any aborted or errored turn parks the continuation loop: an
		// interrupt means the user said stop (continuing immediately would
		// make the loop interrupt-proof), and chaining fresh turns onto a
		// persistent error is unbounded paid retries. The zero-progress audit
		// can't catch either — partial replies were already billed. A later
		// user turn's token progress (or any goal mutation) re-arms.
		if s.goalsEnabled.Load() {
			sess.SuppressGoalContinuation()
		}
		if errors.Is(err, context.Canceled) {
			// Interrupted — finishInterrupted already emitted EventTurnDone,
			// so turn_done + assistant_message were broadcast by the handler.
			// Nothing more for the reply itself.
		} else {
			// A goal-continuation turn failing on provider rate limits parks
			// the goal harder: usage_limited persists and stops continuation
			// until /goal resume. goalContWasPending was captured before the
			// suppression above consumed the pending mark. A rate-limited
			// plain user turn is not parked — the user deserves the bare
			// error first. (A user steer chained onto a not-yet-audited
			// continuation still parks: pending is stale there, but the next
			// continuation would hit the same limit, so parking early is the
			// cheaper outcome.)
			if s.goalsEnabled.Load() && goalContWasPending && agent.IsRateLimitErr(err) {
				if g, gerr := sess.SetGoalStatus(agent.GoalUsageLimited); gerr == nil {
					s.broadcastGoalUpdated(sess.ID, g)
				}
			}
			// Surface the error, then fall through to the common complete +
			// session_update tail. Returning here would skip `complete`, leaving
			// the web UI's streaming flag (and its caret) stuck on forever; the
			// tail's session_update is a superset of what we'd emit here.
			sw.error(err.Error())
		}
		// A first-round failure makes runLoop roll history back past the user
		// message (appendUserInput's error-path contract), and the SyncFrom+
		// Save above erased the crash-safety copy persisted before the turn.
		// The browser still shows that user bubble with a now out-of-range
		// message_index, so a later edit/branch would 400. Tell it to re-fetch
		// the (shorter) transcript so its indices realign with disk. An
		// interrupt no longer shrinks history (finishInterrupted keeps the
		// unanswered user message and caps it with a note — popping it made
		// this very reload blank the transcript), but the gate must still run
		// for both branches: a turn compacted below the watermark reloads too,
		// which also realigns its indices. Gated on an actual shrink below the
		// turn-start watermark (deliberately not the live state's
		// compaction-adjusted copy) so a mid-turn failure that kept the
		// message doesn't trigger a needless reload.
		if len(sess.Messages) < historyWatermark {
			s.broadcastHistoryReload(sess.ID)
		}
	} else {
		// Normal completion: emit the final turn_done explicitly so the
		// frontend gets the aggregated reply even when the provider path
		// doesn't fire EventTurnDone (fallback buffered sender).
		rCopy := reply
		b, _ := json.Marshal(map[string]any{
			"type":       "turn_done",
			"session_id": sess.ID,
			"reply":      map[string]any{"content": rCopy.Content},
		})
		sw.sendRaw(b)
	}

	completeEvent := map[string]any{
		"type":       "complete",
		"session_id": sess.ID,
		"iterations": a.TurnIterations(),
	}
	if err == nil {
		// a is freshly built per turn (buildAgent), so its usage counters start
		// at zero — no before/after diff needed, unlike the CLI/IM persistent-
		// Agent call sites. Omitted on error/interrupt, matching the CLI's
		// summary line which only prints on a clean completion.
		inTok, outTok := a.SessionTokens()
		completeEvent["duration_ms"] = time.Since(turnCallStart).Milliseconds()
		completeEvent["tokens"] = inTok + outTok
	}
	s.wsHub.broadcast(sess.ID, completeEvent)
	// completeEvent above only reaches tabs subscribed to this session; a tab
	// looking at a different session needs this global companion to drive
	// the "agent finished replying" desktop notification.
	s.wsHub.broadcast("", wsEventSessionActivity{
		Type:      "session_activity",
		SessionID: sess.ID,
		Kind:      "turn_complete",
	})

	used, window := a.ContextUsage()
	ctxPct := 0
	if window > 0 {
		ctxPct = used * 100 / window
		if ctxPct > 100 {
			ctxPct = 100
		}
	}
	// Persist the real token count on the session so an idle or resumed session
	// (no live Agent) reports its true context usage — see PersistContextUsage.
	// Best-effort: a save failure just leaves the estimate fallback in place.
	if err := a.PersistContextUsage(sess); err != nil {
		slog.Warn("session: persist context tokens", "session_id", sess.ID, "err", err)
	}

	// Adopt a title generated while the turn ran. The generation goroutine
	// only broadcast + stored it (it must not write the file concurrently with
	// the turn); this is the turn's LAST write, on the single serialized write
	// path, so persisting here is safe and survives. A generation that hasn't
	// finished by now leaves nothing to take — its title rides the next turn's
	// adoption (already broadcast, so the live UI is correct meanwhile). If
	// that session never gets a next turn, the title stays broadcast-only:
	// disk keeps the placeholder and a reload falls back to the message
	// snippet. Acceptable for a best-effort throwaway title; the common
	// long-turn case always finishes generation well before this point.
	if t := s.takePendingTitle(sess.ID); t != "" && agent.IsAutoNamePlaceholder(sess.Title) {
		if terr := sess.SetTitle(t); terr != nil {
			slog.Warn("session title adoption: save failed", "session_id", sess.ID, "err", terr)
		} else {
			// The mid-turn rename broadcast raced the not-yet-persisted title:
			// any client whose list refetch saw the placeholder since then
			// converges on this re-broadcast, now that disk is authoritative.
			s.wsHub.broadcast("", map[string]any{
				"type":       "session_renamed",
				"session_id": sess.ID,
				"name":       t,
			})
		}
	}
	_, pm, re, _, _ := s.sessionStatusFields(sess)
	s.wsHub.broadcast(sess.ID, map[string]any{
		"type":             "session_update",
		"session_id":       sess.ID,
		"status":           "idle",
		"context_usage":    ctxPct,
		"context_tokens":   used,
		"working_dir":      s.sessionCwd(sess),
		"permission_mode":  pm,
		"reasoning_effort": re,
	})

	// After-turn follow-up suggestion (matches TUI suggestCmd behaviour).
	// Fire-and-forget: the frontend shows it as ghost text; failures are silent.
	if err == nil {
		go func() {
			defer s.recoverBg("suggestion generation")
			ctx, cancel := context.WithTimeout(context.Background(), throwawayGenerationTimeout)
			defer cancel()
			text, serr := a.Suggest(ctx, toolDefs)
			if serr != nil || strings.TrimSpace(text) == "" {
				return
			}
			s.wsHub.broadcast(sess.ID, map[string]any{
				"type":       "next_message_suggestion",
				"session_id": sess.ID,
				"text":       text,
			})
		}()
	}
}

// throwawayGenerationTimeout bounds the suggestion-generation call above. A
// confirmed field failure hit "anthropic: send: ... context deadline exceeded"
// on every attempt at the previous 20s timeout: both throwaway calls reuse
// the turn's own Agent/Sender and inherited that session's reasoning_effort
// ("max"), paying the model's full reasoning budget even for a 6-word title.
// The real fix is agent.LowEffortSender (both calls now cap effort to "low"
// instead of inheriting the session's), which removes most of that latency at
// the source — this timeout is only the remaining safety margin for normal
// network/provider variance, not a substitute for the effort cap, so it stays
// modest rather than papering over a slow call with a long wait. (Session
// titles use agent.TitleGenerationTimeout, the shared title mechanism.)
const throwawayGenerationTimeout = 5 * time.Second

// claimTitleGeneration marks a title generation in flight for the session;
// it returns false when one is already running so concurrent turn ends don't
// spend duplicate provider calls. The map is lazily initialised because tests
// build minimal Servers by hand.
func (s *Server) claimTitleGeneration(sessionID string) bool {
	s.titleMu.Lock()
	defer s.titleMu.Unlock()
	if s.titlePending == nil {
		s.titlePending = make(map[string]bool)
	}
	if s.titlePending[sessionID] {
		return false
	}
	s.titlePending[sessionID] = true
	return true
}

func (s *Server) releaseTitleGeneration(sessionID string) {
	s.titleMu.Lock()
	delete(s.titlePending, sessionID)
	s.titleMu.Unlock()
}

// storePendingTitle hands a title generated mid-turn to the turn goroutine,
// which is the ONLY writer of the session file (the generation goroutine must
// not write it concurrently — rewriteAll truncates in place and would corrupt
// the transcript). The turn goroutine persists the stored title at its
// end-of-turn adoption step (see doAgentTurn), on its single serialized write
// path.
func (s *Server) storePendingTitle(sessionID, title string) {
	s.titleMu.Lock()
	if s.pendingTitles == nil {
		s.pendingTitles = make(map[string]string)
	}
	s.pendingTitles[sessionID] = title
	s.titleMu.Unlock()
}

// takePendingTitle returns and clears the title stored by storePendingTitle.
func (s *Server) takePendingTitle(sessionID string) string {
	s.titleMu.Lock()
	defer s.titleMu.Unlock()
	t := s.pendingTitles[sessionID]
	delete(s.pendingTitles, sessionID)
	return t
}

// peekPendingTitle returns the stored title without clearing it. List
// responses overlay it over the not-yet-adopted disk placeholder so a client
// refetch between the mid-turn rename broadcast and the turn-end adoption
// doesn't regress the sidebar to the placeholder.
func (s *Server) peekPendingTitle(sessionID string) string {
	s.titleMu.Lock()
	defer s.titleMu.Unlock()
	return s.pendingTitles[sessionID]
}

// broadcastSessionRenamed announces a session's new title globally so every
// tab's sidebar updates live. Nil-hub safe (broadcastGoalUpdated precedent):
// channel turns run in processes/tests where initWS never ran, and a rename
// is cosmetic — never worth crashing the turn that carries it.
func (s *Server) broadcastSessionRenamed(sessionID, name string) {
	if s.wsHub == nil {
		return
	}
	s.wsHub.broadcast("", map[string]any{
		"type":       "session_renamed",
		"session_id": sessionID,
		"name":       name,
	})
}

// ─── wsStreamWriter ────────────────────────────────────────────────────────

type wsStreamWriter struct {
	sessionID string
	hub       *wsHub
	server    *Server // for live state tracking
	mu        sync.Mutex
}

func (s *Server) newWSStreamWriter(sessionID string) *wsStreamWriter {
	return &wsStreamWriter{
		sessionID: sessionID,
		hub:       s.wsHub,
		server:    s,
	}
}

func (w *wsStreamWriter) sendRaw(data []byte) {
	w.hub.broadcast(w.sessionID, json.RawMessage(data))
}

// error surfaces a turn-level failure — a sender/tool-setup error or an LLM
// call that errored out. It is distinct from EventToolError (per-tool, keyed by
// tool_id): a turn error belongs to no tool card, so the frontend renders it as
// a standalone error notice in the transcript rather than dropping it.
func (w *wsStreamWriter) error(msg string) {
	w.hub.broadcast(w.sessionID, map[string]string{
		"type":       "turn_error",
		"session_id": w.sessionID,
		"error":      msg,
	})
}

// bufferTurnEvent records an already-broadcast turn event in the session's
// live state so replayLiveState can resend it to a tab that subscribes
// mid-turn. Pending deltas are flushed first: anything accumulated by then
// was streamed before this event, so replay order matches broadcast order.
func (w *wsStreamWriter) bufferTurnEvent(ev map[string]any) {
	w.server.liveStateMu.Lock()
	if ls, ok := w.server.liveStates[w.sessionID]; ok {
		ls.flushDeltas(w.sessionID)
		ls.appendEvent(ev)
	}
	w.server.liveStateMu.Unlock()
}

// reseedThinkingProgress broadcasts a fresh "thinking" progress phase and
// records it as the session's live state. Called after a tool finishes (or
// errors): the next LLM round is already running but stays silent until its
// first delta, so without this the indicator the frontend cleared at
// tool_call never comes back between rounds.
func (w *wsStreamWriter) reseedThinkingProgress() {
	startedAt := time.Now().UnixMilli()
	w.server.liveStateMu.Lock()
	if ls, ok := w.server.liveStates[w.sessionID]; ok {
		ls.progress = &wsEventProgress{
			Type:         "progress",
			ProgressType: "thinking",
			Phase:        "active",
			StartedAt:    startedAt,
		}
		// The finished tool's output is done streaming — drop it so a tab
		// subscribing during this "thinking" gap doesn't replay stale stdout
		// under the wrong progress heading.
		ls.stdoutLines = nil
		ls.stdoutToolID = ""
	}
	w.server.liveStateMu.Unlock()
	w.hub.broadcast(w.sessionID, map[string]any{
		"type":          "progress",
		"session_id":    w.sessionID,
		"progress_type": "thinking",
		"phase":         "active",
		"status":        "start",
		"started_at":    startedAt,
	})
}

// handleEvent converts agent.AgentEvent to WS JSON events and broadcasts them.
// It also updates the server's live state for late-subscriber replay.
func (w *wsStreamWriter) handleEvent(ev agent.AgentEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()

	switch ev.Kind {
	case agent.EventTextDelta:
		w.hub.broadcast(w.sessionID, map[string]string{
			"type":       "text_delta",
			"session_id": w.sessionID,
			"text":       ev.Text,
		})
		w.server.liveStateMu.Lock()
		if ls, ok := w.server.liveStates[w.sessionID]; ok {
			ls.textBuf.WriteString(ev.Text)
		}
		w.server.liveStateMu.Unlock()

	case agent.EventToolStarted:
		evt := map[string]any{
			"type":       "tool_call",
			"session_id": w.sessionID,
			"name":       ev.ToolName,
			"args":       ev.Input,
			"tool_id":    ev.ToolID,
		}
		w.hub.broadcast(w.sessionID, evt)

		// Track as live state for replay: fold the finished round's deltas
		// into the buffer first so replay keeps text before its tool call.
		w.server.liveStateMu.Lock()
		if ls, ok := w.server.liveStates[w.sessionID]; ok {
			ls.flushDeltas(w.sessionID)
			ls.appendEvent(evt)
			ls.progress = &wsEventProgress{
				Type:         "progress",
				Message:      ev.ToolName,
				ProgressType: "tool",
				Phase:        "active",
				StartedAt:    time.Now().UnixMilli(),
			}
			// A new tool call starts with no output of its own; the previous
			// call's leftover stdout (if reseedThinkingProgress somehow missed
			// it) must not bleed into this one's replay.
			ls.stdoutLines = nil
			ls.stdoutToolID = ""
		}
		w.server.liveStateMu.Unlock()

	case agent.EventToolProgress:
		// Live output for a running terminal command (issue #1094). Only
		// StreamingToolExecutor tools (currently just terminal) emit this;
		// most tools never reach here. tool_id lets the frontend attribute the
		// chunk to the right card (see #1193's pickToolIndex).
		evt := map[string]any{
			"type":       "tool_stdout",
			"session_id": w.sessionID,
			"tool_id":    ev.ToolID,
			"lines":      []string{ev.Chunk},
		}
		w.hub.broadcast(w.sessionID, evt)
		w.server.liveStateMu.Lock()
		if ls, ok := w.server.liveStates[w.sessionID]; ok {
			ls.stdoutToolID = ev.ToolID
			ls.stdoutLines = append(ls.stdoutLines, ev.Chunk)
			if n := len(ls.stdoutLines) - maxLiveStdoutLines; n > 0 {
				ls.stdoutLines = ls.stdoutLines[n:]
			}
		}
		w.server.liveStateMu.Unlock()

	case agent.EventToolDone:
		toolResult := map[string]any{
			"type":       "tool_result",
			"session_id": w.sessionID,
			"result":     ev.Output,
			"tool_id":    ev.ToolID,
		}
		if ev.UI != nil {
			toolResult["ui_payload"] = ev.UI
			// Task tools surface their checklist as a "todo" UI payload. Also
			// broadcast a dedicated todo_update so the web task panel stays in
			// sync without having to mine tool_result events.
			if m, ok := ev.UI.(map[string]any); ok && m["type"] == "todo" {
				w.hub.broadcast(w.sessionID, wsEventTodoUpdate{
					Type:      "todo_update",
					SessionID: w.sessionID,
					Todos:     m["todos"],
				})
			}
		}
		w.hub.broadcast(w.sessionID, toolResult)
		w.bufferTurnEvent(toolResult)
		// The agent immediately starts the next LLM round after a tool result,
		// and that round emits no event until its first delta (or the next
		// tool_call). Re-seed the "thinking" indicator so the UI animates
		// across the gap instead of the next tool popping out of dead air —
		// same rationale as the turn-start seed in doAgentTurn. The frontend
		// clears it on the next tool_call / assistant_message / complete.
		// (No sub_agent_done broadcast here: in async mode the tool returns
		// while agents still run — the manager's per-agent "done" event is
		// the completion signal for the live panel.)
		w.reseedThinkingProgress()
		// A tool call is the only place a turn starts or kills a background
		// process — refresh the badge.
		w.server.broadcastBackgroundTasks(w.sessionID)

	case agent.EventToolError:
		evt := map[string]any{
			"type":       "tool_error",
			"session_id": w.sessionID,
			"error":      ev.Err,
			"tool_id":    ev.ToolID,
		}
		w.hub.broadcast(w.sessionID, evt)
		w.bufferTurnEvent(evt)
		// A tool error does not end the turn — the error result goes back to
		// the model, which keeps running. Re-seed the indicator just like
		// EventToolDone (deleting the live state here would also blank the
		// progress replay for late-subscribing tabs mid-turn).
		w.reseedThinkingProgress()
		w.server.broadcastBackgroundTasks(w.sessionID)

	case agent.EventThinkingDelta:
		w.hub.broadcast(w.sessionID, map[string]string{
			"type":       "thinking_delta",
			"session_id": w.sessionID,
			"text":       ev.Text,
		})
		w.server.liveStateMu.Lock()
		if ls, ok := w.server.liveStates[w.sessionID]; ok {
			ls.thinkingBuf.WriteString(ev.Text)
		}
		w.server.liveStateMu.Unlock()

	case agent.EventCompactDone:
		// Compaction folded the FoldedMsgs oldest messages into one summary
		// message, shifting every later index down — including the turn's
		// history watermark. Skip no-op compactions (summarization failed or
		// returned nothing; history untouched), signalled by before == after.
		if c := ev.Compact; c != nil && c.FoldedMsgs > 0 && c.BeforeTokens != c.AfterTokens {
			w.server.liveStateMu.Lock()
			if ls, ok := w.server.liveStates[w.sessionID]; ok && ls.historyWatermark > 0 {
				if nw := ls.historyWatermark - c.FoldedMsgs + 1; nw > 1 {
					ls.historyWatermark = nw
				} else {
					// Compaction reached into the current turn: only the
					// summary message itself predates the turn now.
					ls.historyWatermark = 1
				}
			}
			w.server.liveStateMu.Unlock()
		}

	case agent.EventSteerInjected:
		// Prefer the full inbox items (text + attachment blocks) so a steer
		// message's image thumbnails reach the bubble; Messages is the
		// text-only fallback for events from older emitters — those don't
		// stamp SteerBaseIndex, so their bubbles get no message_index rather
		// than a bogus 0+k.
		items := ev.Steer
		indexed := len(items) > 0
		if len(items) == 0 {
			for _, msg := range ev.Messages {
				items = append(items, agent.InboxItem{Text: msg})
			}
		}
		for k, it := range items {
			// <system-reminder> blocks (background-process completion notes,
			// recalled memories) are model-facing context, not user speech —
			// the TUI skips them and the web transcript must too.
			text := strings.TrimSpace(agent.StripSystemReminders(it.Text))
			imgs := imageRefsFromBlocks(it.Blocks)
			if text == "" && len(imgs) == 0 {
				continue
			}
			evt := map[string]any{
				"type":       "history_user_message",
				"session_id": w.sessionID,
				"content":    text,
				"created_at": time.Now().UnixMilli(),
			}
			if indexed {
				// Position in the persisted Messages array so edit/branch can
				// target a steered message too. Item k lands at SteerBaseIndex+k;
				// reminder-only items skipped above still occupy a history slot,
				// so the loop index k (not a compacted counter) is authoritative.
				evt["message_index"] = ev.SteerBaseIndex + k
			}
			if len(imgs) > 0 {
				evt["images"] = imgs
			}
			w.hub.broadcast(w.sessionID, evt)
			// Steer messages persist only at turn end like everything else
			// in the turn — buffer them so a refresh keeps the bubble.
			w.bufferTurnEvent(evt)
		}

	case agent.EventGoalUpdated:
		if ev.Goal != nil {
			w.server.broadcastGoalUpdated(w.sessionID, *ev.Goal)
		}

	case agent.EventTurnDone:
		if ev.Reply != nil {
			w.hub.broadcast(w.sessionID, map[string]any{
				"type":       "turn_done",
				"session_id": w.sessionID,
				"reply":      map[string]any{"content": ev.Reply.Content},
			})
			// Frontend expects a complete assistant_message event rather than
			// streaming text_delta fragments. Emit it once the turn is done.
			w.hub.broadcast(w.sessionID, map[string]any{
				"type":       "assistant_message",
				"session_id": w.sessionID,
				"content":    ev.Reply.Content,
				"thinking":   extractThinking(ev.Reply),
			})
		}
		// Live state is NOT cleared here: this event fires inside RunStream,
		// before doAgentTurn persists the session, and a tab subscribing in
		// that gap needs the replay buffer (history doesn't have the turn
		// yet). doAgentTurn drops the state right after Save.
	}
}

// ─── Server fields for WS ──────────────────────────────────────────────────

// ws fields added to Server.
func (s *Server) initWS() {
	s.wsHub = newWSHub()
	s.wsHub.init(s)
	s.interrupts = make(map[string]context.CancelFunc)
	s.confirmations = make(map[string]chan string)
	s.liveStates = make(map[string]*sessionLiveState)
}

// toolDefsFor returns tool definitions for the given model.
func (s *Server) toolDefsFor(model string) []agent.ToolDefinition {
	// Import cycle avoidance — tools.DefaultToolsFor is called from handlers.go
	return getDefaultToolsFor(model)
}

// Interrupt cancellation tracking.
func (s *Server) registerInterrupt(sessionID string, cancel context.CancelFunc) {
	s.interruptMu.Lock()
	s.interrupts[sessionID] = cancel
	s.interruptMu.Unlock()
}

// sessionStatus returns the status string the frontend keys on ("running"
// shows the interrupt button; anything else hides it). A registered interrupt
// cancel func exists exactly for the duration of a turn — read via its own
// mutex, unlike the turnRunning map which is guarded by per-session turnLocks.
func (s *Server) sessionStatus(sessionID string) string {
	s.interruptMu.Lock()
	defer s.interruptMu.Unlock()
	if _, ok := s.interrupts[sessionID]; ok {
		return "running"
	}
	return "idle"
}

// acquireAskSlot blocks until this session's interactive-prompt slot is free,
// then returns a release func. Only one confirmation or question is outstanding
// per session at a time, so concurrent askers queue instead of clobbering each
// other (see Server.askSlots). It respects ctx cancellation so a cancelled turn
// doesn't wait behind a prompt that may sit until its timeout.
func (s *Server) acquireAskSlot(ctx context.Context, sessionID string) (func(), error) {
	s.askSlotsMu.Lock()
	if s.askSlots == nil {
		s.askSlots = make(map[string]chan struct{})
	}
	sem, ok := s.askSlots[sessionID]
	if !ok {
		sem = make(chan struct{}, 1)
		s.askSlots[sessionID] = sem
	}
	s.askSlotsMu.Unlock()

	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// confirmDetail carries the "what am I actually approving" fields for a
// permission ask (#1105). At most one of Command/Diff/Input is populated,
// chosen by tool kind in permissionAskFrom.
type confirmDetail struct {
	ToolName string
	Command  string
	Diff     string
	Input    string
}

// Request confirmation from user (blocks until user responds in browser or ctx
// is cancelled).
func (s *Server) requestConfirmation(ctx context.Context, sessionID, message, kind string, detail confirmDetail) (string, error) {
	release, err := s.acquireAskSlot(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("confirmation cancelled")
	}
	defer release()

	confID := fmt.Sprintf("conf_%d", time.Now().UnixNano())
	ch := make(chan string, 1)

	s.confirmMu.Lock()
	s.confirmations[confID] = ch
	s.confirmMu.Unlock()

	ev := wsEventRequestConfirmation{
		Type:      "request_confirmation",
		SessionID: sessionID,
		ConfID:    confID,
		Message:   message,
		Kind:      kind,
		ToolName:  detail.ToolName,
		Command:   detail.Command,
		Diff:      detail.Diff,
		Input:     detail.Input,
	}

	// Record the outstanding confirmation so a tab that (re)subscribes
	// mid-ask — e.g. after a page refresh — gets it replayed.
	s.pendingPromptMu.Lock()
	s.pendingConfirms[sessionID] = ev
	s.pendingPromptMu.Unlock()

	cleanup := func() {
		s.confirmMu.Lock()
		delete(s.confirmations, confID)
		s.confirmMu.Unlock()
		s.pendingPromptMu.Lock()
		delete(s.pendingConfirms, sessionID)
		s.pendingPromptMu.Unlock()
	}

	s.wsHub.broadcast(sessionID, ev)

	// Wait for response, timeout, or cancellation.
	select {
	case result := <-ch:
		cleanup()
		return result, nil
	case <-ctx.Done():
		cleanup()
		return "", fmt.Errorf("confirmation cancelled")
	case <-time.After(5 * time.Minute):
		cleanup()
		return "", fmt.Errorf("confirmation timed out")
	}
}

// extractThinking pulls a reasoning/thinking trace from a Reply's Blocks so the
// web UI can render it alongside the final assistant message. Anthropic models
// return it as a standalone "thinking" block; OpenAI models stash it on the
// first "tool_use" block (Reasoning field). Empty string when none is present.
func extractThinking(reply *agent.Reply) string {
	if reply == nil {
		return ""
	}
	for _, b := range reply.Blocks {
		if b.Type == "thinking" && b.Thinking != "" {
			return b.Thinking
		}
		if b.Type == "tool_use" && b.Reasoning != "" {
			return b.Reasoning
		}
	}
	return ""
}
