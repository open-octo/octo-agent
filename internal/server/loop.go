package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/channel"
	"github.com/open-octo/octo-agent/internal/tools"
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
		s.deliverLoopTick(sessionID, prompt, repeat)
	})
}

// deliverLoopTick injects one loop tick as a user steer and kicks an idle turn.
// In interval mode it no-ops while a turn is already running: kickIdleSteerTurn
// bails when busy, so the steer would sit undrained and every later tick would
// stack another identical copy for the running turn to replay. The interval
// timer has already re-armed, so the next tick delivers once the session is
// idle — mirroring the TUI, which only re-arms (never queues) while a turn runs.
//
// The prompt is wrapped as a <system-reminder> (formatLoopTick) so the web
// transcript renders the tick as an environment re-entry rather than a
// duplicated user-message bubble every tick — matching the TUI, which prints a
// "● Loop tick" line and never echoes the prompt as user speech. The scrollback
// notice is broadcast only when kickIdleSteerTurn actually starts a turn, so a
// tick folded into an already-running turn — or one dropped because another
// entry took the session over — shows nothing, avoiding a "Loop tick" marker
// with no reply behind it. This mirrors the TUI, which prints the line only when
// the wakeup actually starts a turn.
func (s *Server) deliverLoopTick(sessionID, prompt string, repeat bool) {
	if s.shouldSkipTick(sessionID, repeat) {
		return
	}
	s.enqueueSteer(sessionID, agent.InboxItem{Text: formatLoopTick(prompt)})
	if s.kickIdleSteerTurn(sessionID) {
		s.broadcastLoopTick(sessionID)
	}
}

// formatLoopTick wraps a loop prompt as a <system-reminder> block — octo's
// convention for injected, non-user context that UIs strip from user-visible
// text (see FormatBgNote). This is what keeps the web transcript from rendering
// the prompt as a fresh user bubble on every tick: doAgentTurn computes the
// visible bubble text from StripSystemReminders(content), which is empty here,
// so no history_user_message is broadcast (live) or reconstructed (on reload).
// The model still reads and acts on it — doAgentTurn runs the turn on the full
// wrapped content, and an idle turn whose entire user message is a
// system-reminder still produces a reply (the same path background- and
// sub-agent-completion notes use). The preamble tells the model to treat the
// task as if the user just sent it, so a directive prompt isn't mistaken for
// passive context.
func formatLoopTick(prompt string) string {
	return "<system-reminder>\n" +
		"[LOOP TICK] Your scheduled loop fired. Continue the task below as if the user just sent it:\n\n" +
		prompt +
		"\n</system-reminder>"
}

// broadcastLoopTick emits the "Loop tick" scrollback notice to a web session,
// mirroring the TUI line printed when a wakeup fires. No-op without a wsHub (IM
// sessions, tests): the IM path surfaces the model's reply in the chat instead,
// so it needs no separate tick marker.
func (s *Server) broadcastLoopTick(sessionID string) {
	if s.wsHub == nil {
		return
	}
	s.wsHub.broadcast(sessionID, wsEventLoopTickNotice{
		Type:      "loop_tick_notice",
		SessionID: sessionID,
	})
}

// shouldSkipTick reports whether this tick's delivery should be dropped because
// a turn is already running. Only interval mode (repeat) skips: its timer
// re-arms independently, so a dropped tick is retried next cadence. A dynamic
// tick (repeat=false) fires exactly once and does NOT re-arm — dropping it would
// silently kill the loop, so it always delivers; the running turn drains the
// steer at its next chain boundary and the model re-arms from there.
func (s *Server) shouldSkipTick(sessionID string, repeat bool) bool {
	return repeat && s.turnActive(sessionID)
}

// turnActive reports whether a turn is currently running for the session,
// reading turnRunning under the session's turn lock (its guarding mutex).
func (s *Server) turnActive(sessionID string) bool {
	mu := s.sessionTurnLock(sessionID)
	mu.Lock()
	defer mu.Unlock()
	return s.turnRunning[sessionID]
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
