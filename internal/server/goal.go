package server

import (
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
