package server

import (
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/tools"
)

// serverWaker implements tools.Waker for a server-managed session. On wakeup it
// enqueues the loop prompt as a user steer and kicks an idle turn — the same
// idle auto-turn path that background-process and sub-agent completion notes
// use — so the model re-enters the loop without the user re-prompting. Interval
// mode (repeat) re-arms the timer from inside the fired callback; a new user
// turn or an interrupt cancels it (cancelWakeup), matching the TUI and Claude
// Code's loop. It is stamped into the turn ctx in runAgentTurnLoop.
type serverWaker struct {
	s         *Server
	sessionID string
}

func (w serverWaker) ScheduleWakeup(delay time.Duration, prompt, reason string, repeat bool) error {
	w.s.armWakeup(w.sessionID, delay, prompt, repeat)
	return nil
}

// armWakeup (re)starts the session's loop wakeup timer, replacing any pending
// one — a session holds at most one armed wakeup at a time.
func (s *Server) armWakeup(sessionID string, delay time.Duration, prompt string, repeat bool) {
	s.wakeupMu.Lock()
	defer s.wakeupMu.Unlock()
	if t := s.wakeupTimers[sessionID]; t != nil {
		t.Stop()
	}
	s.wakeupTimers[sessionID] = time.AfterFunc(delay, func() {
		// Interval mode: re-arm for the next tick before delivering, so the
		// cadence is independent of how long the woken turn runs. Dynamic mode
		// fires once and is re-armed (or not) by the model's next turn.
		if repeat {
			s.armWakeup(sessionID, delay, prompt, true)
		} else {
			s.cancelWakeup(sessionID)
		}
		s.enqueueSteer(sessionID, agent.InboxItem{Text: prompt})
		s.kickIdleSteerTurn(sessionID)
	})
}

// cancelWakeup stops and forgets a session's pending loop wakeup, if any.
// Called when the user takes over (a new message, a retry, or an interrupt).
func (s *Server) cancelWakeup(sessionID string) {
	s.wakeupMu.Lock()
	defer s.wakeupMu.Unlock()
	if t := s.wakeupTimers[sessionID]; t != nil {
		t.Stop()
		delete(s.wakeupTimers, sessionID)
	}
}

// wakerFor returns the Waker stamped into a server session's turn ctx.
func (s *Server) wakerFor(sessionID string) tools.Waker {
	return serverWaker{s: s, sessionID: sessionID}
}
