package server

import (
	"fmt"
	"net/http"

	"github.com/open-octo/octo-agent/internal/agent"
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
}

// broadcastGoalCleared tells subscribers the goal is gone (nil payload — the
// same shape a cleared goal has in the session record).
func (s *Server) broadcastGoalCleared(sessionID string) {
	if s.wsHub == nil {
		return
	}
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

// wsGoalCommand applies a "/goal …" slash command from the web composer and
// reports the outcome as a toast plus a goal_updated broadcast so every tab's
// chip refreshes.
func (s *Server) wsGoalCommand(sessionID, args string) {
	if !s.goalsEnabled.Load() {
		s.wsToast(sessionID, "Goals are disabled (goal.enabled)", "error")
		return
	}
	sess, err := s.goalSession(sessionID)
	if err != nil {
		s.wsToast(sessionID, fmt.Sprintf("/goal: %v", err), "error")
		return
	}
	reply := agent.GoalCommand(sess, args)
	level := "info"
	if len(reply) > 6 && (reply[:6] == "/goal:" || reply[:6] == "/goal ") {
		level = "error"
	}
	s.wsToast(sessionID, reply, level)
	if g, ok := sess.GoalSnapshot(); ok {
		s.broadcastGoalUpdated(sessionID, g)
	} else {
		s.broadcastGoalCleared(sessionID)
	}
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
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

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
