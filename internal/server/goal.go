package server

import (
	"strings"

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

// isRateLimitErr classifies a turn error as a provider rate/quota limit.
// Provider adapters surface non-2xx responses as "<vendor>: HTTP <code>: …"
// (the retry layer has already retried transient 429s by the time one
// reaches here), so a sustained limit is matched on the status code plus the
// common textual variants gateways use.
func isRateLimitErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "http 429") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "rate_limit") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "quota")
}
