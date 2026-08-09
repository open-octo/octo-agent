package server

import (
	"fmt"
	"net/http"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// steerPending reports whether user steer input is queued for the session,
// without draining it. The goal-continuation kick defers to queued user
// input — a continuation prompt must never displace or delay what the user
// just said.
func (s *Server) steerPending(sessionID string) bool {
	s.steerMu.Lock()
	defer s.steerMu.Unlock()
	return len(s.steerQueues[sessionID]) > 0
}

// goalWorkPending reports whether the session has async work in flight that
// the goal-continuation kick must wait for — a one-shot background process or
// a running async sub-agent. Continuing on top of one only buys a turn that
// says "still waiting" and bills for it; the completion hook starts its own
// turn, and that turn's end re-enters the continuation check.
func (s *Server) goalWorkPending(sessionID string) bool {
	return tools.PendingAsyncWork(
		tools.SessionBackgroundManager(sessionID),
		tools.SessionSubAgentManager(sessionID, nil),
	)
}

// broadcastGoalUpdated pushes the goal snapshot to the session's subscribers.
// Fired on every in-turn accounting change (via EventGoalUpdated) and on the
// server-owned transitions (usage_limited).
func (s *Server) broadcastGoalUpdated(sessionID string, g agent.Goal) {
	if s.wsHub == nil {
		return
	}
	s.wsHub.broadcast(sessionID, map[string]any{
		"type":       "goal_updated",
		"session_id": sessionID,
		"goal":       g,
	})
	s.noticeGoalTransition(sessionID, g)
}

// noticeGoalTransition emits the scrollback line for the statuses that end or
// stall a goal, once per transition into them. Accounting fires this broadcast
// on every tick, so the last status seen per session is what makes it a
// transition — the same comparison the TUI makes against its own last-seen
// status. A session with no recorded status yet is only recorded, never
// announced: a page that connects to an already-blocked goal must not replay
// the transition that blocked it.
//
// The wording is deliberately built here rather than in the browser: it shares
// agent's formatters with the TUI lines it mirrors, so the two can't drift.
func (s *Server) noticeGoalTransition(sessionID string, g agent.Goal) {
	s.goalStatusMu.Lock()
	if s.goalLastStatus == nil {
		// Hand-built Servers in tests skip New's map init.
		s.goalLastStatus = make(map[string]agent.GoalStatus)
	}
	prev, seen := s.goalLastStatus[sessionID]
	s.goalLastStatus[sessionID] = g.Status
	s.goalStatusMu.Unlock()
	if !seen || prev == g.Status {
		return
	}
	var text, level string
	switch g.Status {
	case agent.GoalBudgetLimited:
		text = fmt.Sprintf("Goal budget reached (%s/%s tokens) — wrapping up",
			agent.FormatGoalTokens(g.TokensUsed), agent.FormatGoalTokens(g.TokenBudget))
		level = "warning"
	case agent.GoalComplete:
		text = "Goal complete — " + agent.GoalUsageLine(g)
		level = "success"
	case agent.GoalBlocked:
		text = "Goal blocked — the agent is at an impasse; /goal resume to retry"
		level = "warning"
	case agent.GoalUsageLimited:
		text = "Goal usage limited (provider rate limit) — /goal resume to retry"
		level = "warning"
	default:
		// active / paused are always the direct result of a command, which
		// reports itself. Announcing them here would double up.
		return
	}
	s.broadcastGoalNotice(sessionID, "status", text, level)
}

// broadcastGoalCleared tells subscribers the goal is gone (nil payload — the
// same shape a cleared goal has in the session record).
func (s *Server) broadcastGoalCleared(sessionID string) {
	if s.wsHub == nil {
		return
	}
	// Forget the last status too: a goal created later starts a fresh
	// transition history, so its first status is a baseline, not a change.
	s.goalStatusMu.Lock()
	delete(s.goalLastStatus, sessionID)
	s.goalStatusMu.Unlock()
	s.wsHub.broadcast(sessionID, map[string]any{
		"type":       "goal_updated",
		"session_id": sessionID,
		"goal":       nil,
	})
}

// goalSession returns the Session object goal reads/mutations must target:
// the live one when a turn is running (mutating a freshly-loaded copy would
// be overwritten by the running turn's own goal records), else a fresh load.
func (s *Server) goalSession(sessionID string) (*agent.Session, error) {
	s.sessionAgentsMu.Lock()
	live := s.liveSessions[sessionID]
	s.sessionAgentsMu.Unlock()
	if live != nil {
		return live, nil
	}
	return agent.LoadSession(sessionID)
}

// broadcastGoalNotice emits a "● Goal …" scrollback line to the session's web
// subscribers, the counterpart of the TUI's goal notices. No-op without a
// wsHub (IM sessions, tests).
func (s *Server) broadcastGoalNotice(sessionID, kind, text, level string) {
	if s.wsHub == nil {
		return
	}
	s.wsHub.broadcast(sessionID, wsEventGoalNotice{
		Type:      "goal_notice",
		SessionID: sessionID,
		Kind:      kind,
		Text:      text,
		Level:     level,
	})
}

// wsGoalCommand applies a "/goal …" slash command from the web composer and
// reports the outcome as a scrollback notice plus a goal_updated broadcast so
// every tab's chip refreshes. The reply lands in the transcript rather than a
// toast: it is the TUI's scrollback line, and a goal outlives the seconds a
// toast is on screen.
func (s *Server) wsGoalCommand(sessionID, args string) {
	if !s.goalsEnabled.Load() {
		s.broadcastGoalNotice(sessionID, "command", "Goals are disabled (goal.enabled)", "error")
		return
	}
	sess, err := s.goalSession(sessionID)
	if err != nil {
		s.broadcastGoalNotice(sessionID, "command", fmt.Sprintf("/goal: %v", err), "error")
		return
	}
	titleBefore := sess.Title
	reply, start := agent.GoalCommand(sess, args)
	level := "info"
	if len(reply) > 6 && (reply[:6] == "/goal:" || reply[:6] == "/goal ") {
		level = "error"
	}
	s.broadcastGoalNotice(sessionID, "command", reply, level)
	// Creating or replacing a goal seeds a placeholder-named session's title
	// from the objective (startGoalLocked, which also persists it). Tell the
	// sidebar: without this the rename would only surface on the next refetch,
	// so a session started by "/goal …" would look unnamed for as long as the
	// tab stayed open.
	if sess.Title != titleBefore {
		s.broadcastSessionRenamed(sessionID, sess.Title)
	}
	if g, ok := sess.GoalSnapshot(); ok {
		s.broadcastGoalUpdated(sessionID, g)
	} else {
		s.broadcastGoalCleared(sessionID)
	}
	if start != agent.GoalStartNone {
		s.kickIdleGoalTurn(sessionID, start)
	}
}

// kickIdleGoalTurn starts the goal's continuation turn right away when the
// session is idle, so a goal created, replaced, or resumed from the web
// composer begins work at once instead of waiting for the user's next message
// — the TUI's startGoalNow behavior. A running turn needs no kick:
// runAgentTurnLoop consults GoalContinuation at every turn end.
//
// The notice is emitted only once the kick is committed, from inside the
// callback: announcing "Goal starts" and then finding the session busy or the
// continuation suppressed would promise a turn that never runs.
func (s *Server) kickIdleGoalTurn(sessionID string, start agent.GoalCommandStart) bool {
	return s.kickIdleTurn(sessionID, func(sess *agent.Session) (string, []agent.ContentBlock, bool) {
		// Queued user input outranks the continuation, the same rule the
		// turn-end kick follows: let that input run and its own turn end pick
		// the goal back up.
		if s.steerPending(sessionID) {
			return "", nil, false
		}
		prompt, ok := sess.GoalContinuation()
		if !ok {
			return "", nil, false
		}
		kind := "continue"
		if start == agent.GoalStartFresh {
			kind = "start"
		}
		s.broadcastGoalNotice(sessionID, kind, "", "info")
		return prompt, nil, true
	})
}

// handleGetSessionGoal serves GET /api/sessions/{id}/goal → {goal: …|null}.
func (s *Server) handleGetSessionGoal(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.goalSessionForRequest(w, r)
	if !ok {
		return
	}
	if g, exists := sess.GoalSnapshot(); exists {
		writeJSON(w, http.StatusOK, map[string]any{"goal": g})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"goal": nil})
}

// handleUpdateSessionGoal serves PUT /api/sessions/{id}/goal — the Codex
// thread/goal/set equivalent. Objective alone creates or edits; status alone
// pauses/resumes; replace=true mints a fresh goal with the given objective
// and optional budget.
func (s *Server) handleUpdateSessionGoal(w http.ResponseWriter, r *http.Request) {
	if !s.goalsEnabled.Load() {
		writeError(w, http.StatusForbidden, "goals are disabled (goal.enabled)")
		return
	}
	sess, ok := s.goalSessionForRequest(w, r)
	if !ok {
		return
	}
	var req struct {
		Objective   string `json:"objective"`
		Status      string `json:"status"`
		TokenBudget int64  `json:"token_budget"`
		Replace     bool   `json:"replace"`
	}
	if err := readBodyJSON(r, &req); err != nil {
		writeInvalidJSONBody(w, err)
		return
	}

	titleBefore := sess.Title
	var g agent.Goal
	var err error
	switch {
	case req.Replace:
		g, err = sess.ReplaceGoal(req.Objective, req.TokenBudget)
	case req.Objective != "":
		if _, exists := sess.GoalSnapshot(); exists {
			g, err = sess.EditGoalObjective(req.Objective)
		} else {
			g, err = sess.CreateGoal(req.Objective, req.TokenBudget)
		}
	case req.Status != "":
		// The API is user-owned surface: it may pause and resume. The
		// model-owned and system-owned transitions stay with their owners.
		switch agent.GoalStatus(req.Status) {
		case agent.GoalPaused, agent.GoalActive:
			g, err = sess.SetGoalStatus(agent.GoalStatus(req.Status))
		default:
			writeError(w, http.StatusBadRequest, "status must be \"active\" or \"paused\"")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "provide objective, status, or replace")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.broadcastGoalUpdated(sess.ID, g)
	// Same objective-seeded rename as the composer's /goal (see wsGoalCommand).
	if sess.Title != titleBefore {
		s.broadcastSessionRenamed(sess.ID, sess.Title)
	}
	writeJSON(w, http.StatusOK, map[string]any{"goal": g})
}

// handleDeleteSessionGoal serves DELETE /api/sessions/{id}/goal.
func (s *Server) handleDeleteSessionGoal(w http.ResponseWriter, r *http.Request) {
	if !s.goalsEnabled.Load() {
		writeError(w, http.StatusForbidden, "goals are disabled (goal.enabled)")
		return
	}
	sess, ok := s.goalSessionForRequest(w, r)
	if !ok {
		return
	}
	cleared := sess.ClearGoal()
	if cleared {
		s.broadcastGoalCleared(sess.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"cleared": cleared})
}

// goalSessionForRequest resolves {id} to the goal-mutation target session,
// writing the HTTP error itself when the session is missing.
func (s *Server) goalSessionForRequest(w http.ResponseWriter, r *http.Request) (*agent.Session, bool) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return nil, false
	}
	sess, err := s.goalSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return nil, false
	}
	return sess, true
}
