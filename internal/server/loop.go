package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/channel"
	"github.com/Leihb/octo-agent/internal/tools"
)

// serverWaker implements tools.Waker for a server-managed session. On wakeup it
// enqueues the loop prompt as a user steer and kicks an idle turn — the same
// idle auto-turn path that background-process and sub-agent completion notes
// use — so the model re-enters the loop without the user re-prompting. Interval
// mode (repeat) re-arms the timer from inside the fired callback. The loop
// coexists with user messages (CC-style); it stops only on an explicit
// interrupt or schedule_wakeup(cancel=true). Stamped into the turn ctx in
// runAgentTurnLoop.
type serverWaker struct {
	s         *Server
	sessionID string
}

func (w serverWaker) ScheduleWakeup(delay time.Duration, prompt, reason string, repeat bool) error {
	w.s.armWakeup(w.sessionID, delay, prompt, repeat)
	return nil
}

func (w serverWaker) CancelWakeup() error {
	w.s.cancelWakeup(w.sessionID)
	return nil
}

// armWakeup (re)starts a web session's loop wakeup timer (keyed by session ID).
// On fire it injects the loop prompt as a user steer and kicks an idle turn —
// the same path background/sub-agent completion notes use.
func (s *Server) armWakeup(sessionID string, delay time.Duration, prompt string, repeat bool) {
	s.armWakeupFn(sessionID, delay, repeat, func() {
		s.enqueueSteer(sessionID, agent.InboxItem{Text: prompt})
		s.kickIdleSteerTurn(sessionID)
	})
}

// armWakeupFn (re)starts the loop wakeup timer for key, replacing any pending
// one — a session holds at most one armed wakeup. fire is the surface-specific
// delivery (web steer-kick, or IM Inbox + idle channel turn). Interval mode
// (repeat) re-arms from inside the fired callback so the cadence is independent
// of how long the woken turn runs; dynamic mode fires once.
//
// Anti-leak: wakeupStart[key] is stamped on the first arm and kept across ticks
// (the fired callback clears only the spent timer, not the start), so once the
// loop has run past tools.MaxLoopLifetime it stops instead of re-arming — the
// same bound the TUI enforces. A forgotten server-side loop can't tick forever.
func (s *Server) armWakeupFn(key string, delay time.Duration, repeat bool, fire func()) {
	s.wakeupMu.Lock()
	defer s.wakeupMu.Unlock()
	if s.wakeupStart[key].IsZero() {
		s.wakeupStart[key] = time.Now()
	}
	if tools.LoopExpired(s.wakeupStart[key]) {
		s.stopWakeupLocked(key)
		slog.Info("loop stopped: reached max runtime", "session", key, "max", tools.MaxLoopLifetime)
		return
	}
	if t := s.wakeupTimers[key]; t != nil {
		t.Stop()
	}
	s.wakeupTimers[key] = time.AfterFunc(delay, func() {
		// This timer is spent. Drop it but KEEP wakeupStart so the lifetime
		// accumulates across ticks (dynamic mode re-arms via the model; the
		// clock must not reset each tick).
		s.wakeupMu.Lock()
		delete(s.wakeupTimers, key)
		s.wakeupMu.Unlock()
		if repeat {
			s.armWakeupFn(key, delay, repeat, fire)
		}
		fire()
	})
}

// cancelWakeup stops a session's loop and resets its anti-leak clock. Called on
// an explicit stop: an interrupt, schedule_wakeup(cancel=true), or a context
// reset (/clear, /new, /bind, /unbind).
func (s *Server) cancelWakeup(sessionID string) {
	s.wakeupMu.Lock()
	defer s.wakeupMu.Unlock()
	s.stopWakeupLocked(sessionID)
}

// stopWakeupLocked tears down a session's timer and clock. Caller holds wakeupMu.
func (s *Server) stopWakeupLocked(key string) {
	if t := s.wakeupTimers[key]; t != nil {
		t.Stop()
		delete(s.wakeupTimers, key)
	}
	delete(s.wakeupStart, key)
}

// clearWakeupClockIfIdle drops a session's anti-leak clock once its loop has
// truly ended — i.e. no wakeup is armed. Called after an idle/wakeup-triggered
// turn completes: an interval loop re-armed before the turn (timer present →
// kept); a dynamic loop the model chose not to continue leaves no timer, so its
// clock is reclaimed here instead of lingering in wakeupStart forever. A no-op
// for ordinary (non-loop) idle steers, which never set a clock.
func (s *Server) clearWakeupClockIfIdle(key string) {
	s.wakeupMu.Lock()
	defer s.wakeupMu.Unlock()
	if s.wakeupTimers[key] == nil {
		delete(s.wakeupStart, key)
	}
}

// stopAllWakeups stops every armed loop and clears all clocks. Called on
// shutdown so no timer callback races teardown (adapters closing, sessions
// going away) and the maps don't outlive the process intent.
func (s *Server) stopAllWakeups() {
	s.wakeupMu.Lock()
	defer s.wakeupMu.Unlock()
	for k, t := range s.wakeupTimers {
		if t != nil {
			t.Stop()
		}
		delete(s.wakeupTimers, k)
	}
	for k := range s.wakeupStart {
		delete(s.wakeupStart, k)
	}
}

// wakerFor returns the Waker stamped into a web session's turn ctx.
func (s *Server) wakerFor(sessionID string) tools.Waker {
	return serverWaker{s: s, sessionID: sessionID}
}

// imWaker implements tools.Waker for an IM (channel) session. The reply must go
// through the adapter, not the web wsHub, so on wakeup it enqueues the loop
// prompt into the session's Inbox and launches a channel idle turn — the same
// path async completion notes use to reach an IM user. Timers are keyed by the
// session's "im:<key>" namespace so they never collide with web session IDs.
type imWaker struct {
	s    *Server
	sess *channel.Session
	ad   channel.Adapter
	ev   channel.InboundEvent
}

func imWakeupKey(sess *channel.Session) string { return "im:" + string(sess.Key) }

func (w imWaker) ScheduleWakeup(delay time.Duration, prompt, reason string, repeat bool) error {
	sess, ad, ev := w.sess, w.ad, w.ev
	w.s.armWakeupFn(imWakeupKey(sess), delay, repeat, func() {
		sess.Agent.Inbox.Enqueue(prompt)
		go w.s.runChannelIdleTurn(context.Background(), sess, ad, ev)
	})
	return nil
}

func (w imWaker) CancelWakeup() error {
	w.s.cancelWakeup(imWakeupKey(w.sess))
	return nil
}
