package server

import (
	"testing"
	"time"

	"github.com/Leihb/octo-agent/internal/channel"
	"github.com/Leihb/octo-agent/internal/tools"
)

// Long delays keep the timer from firing during the test, so we exercise the
// arm/cancel bookkeeping without the live-session delivery path.

func TestServerWaker_ArmAndCancel(t *testing.T) {
	s := newLoopTestServer()
	s.armWakeup("sid", time.Hour, "p", false)
	if n := s.armedCount(); n != 1 {
		t.Fatalf("expected 1 armed timer after arm, got %d", n)
	}
	s.cancelWakeup("sid")
	if n := s.armedCount(); n != 0 {
		t.Fatalf("cancel should remove the timer, got %d", n)
	}
}

func TestServerWaker_ReArmReplaces(t *testing.T) {
	s := newLoopTestServer()
	s.armWakeup("sid", time.Hour, "p", false)
	first := s.wakeupTimers["sid"]
	s.armWakeup("sid", time.Hour, "p2", true)
	if n := s.armedCount(); n != 1 {
		t.Fatalf("re-arm should not add a second timer, got %d", n)
	}
	if s.wakeupTimers["sid"] == first {
		t.Fatal("re-arm should replace the timer instance")
	}
	s.cancelWakeup("sid")
}

func TestServerWaker_ImplementsWaker(t *testing.T) {
	s := newLoopTestServer()
	var w tools.Waker = s.wakerFor("sid")
	if err := w.ScheduleWakeup(time.Hour, "p", "r", false); err != nil {
		t.Fatalf("ScheduleWakeup: %v", err)
	}
	if n := s.armedCount(); n != 1 {
		t.Fatalf("ScheduleWakeup should arm a timer, got %d", n)
	}
	if err := w.CancelWakeup(); err != nil {
		t.Fatalf("CancelWakeup: %v", err)
	}
	if n := s.armedCount(); n != 0 {
		t.Fatalf("CancelWakeup should clear the timer, got %d", n)
	}
}

// imWaker arms under the "im:<key>" namespace and delivers through the channel
// path. A long delay keeps the fire callback (which needs a live Agent) from
// running, so we test the arm/cancel bookkeeping only.
func TestIMWaker_ArmAndCancel(t *testing.T) {
	s := newLoopTestServer()
	sess := &channel.Session{Key: channel.SessionKey("k")}
	var w tools.Waker = imWaker{s: s, sess: sess}
	if err := w.ScheduleWakeup(time.Hour, "tick", "r", true); err != nil {
		t.Fatalf("ScheduleWakeup: %v", err)
	}
	if _, ok := s.wakeupTimers["im:k"]; !ok {
		t.Fatal("imWaker should arm a timer under the im: namespace")
	}
	if err := w.CancelWakeup(); err != nil {
		t.Fatalf("CancelWakeup: %v", err)
	}
	if n := s.armedCount(); n != 0 {
		t.Fatalf("CancelWakeup should clear the timer, got %d", n)
	}
}

// Anti-leak: a loop whose clock is already past MaxLoopLifetime stops on its
// next (re)arm instead of scheduling another tick.
func TestServerWaker_MaxLifetimeStops(t *testing.T) {
	s := newLoopTestServer()
	// Pre-seed an expired start, then re-arm as the interval timer would.
	s.wakeupStart["sid"] = time.Now().Add(-2 * tools.MaxLoopLifetime)
	s.armWakeup("sid", time.Hour, "p", true)
	if n := s.armedCount(); n != 0 {
		t.Fatalf("an expired loop must not re-arm, got %d armed", n)
	}
	if _, ok := s.wakeupStart["sid"]; ok {
		t.Fatal("stopping should clear the loop clock")
	}
}

// armWakeupFn actually fires its callback and, in interval mode, re-arms.
func TestServerWaker_FiresAndReArms(t *testing.T) {
	s := newLoopTestServer()
	fired := make(chan struct{}, 8)
	s.armWakeupFn("sid", 5*time.Millisecond, true, func() { fired <- struct{}{} })

	// First tick.
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("interval wakeup never fired")
	}
	// Interval mode re-arms itself → a second tick arrives without re-calling arm.
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("interval wakeup did not re-arm for a second tick")
	}
	s.cancelWakeup("sid")
}

// A dynamic (repeat=false) wakeup fires once and does not re-arm itself.
func TestServerWaker_DynamicFiresOnce(t *testing.T) {
	s := newLoopTestServer()
	fired := make(chan struct{}, 8)
	s.armWakeupFn("sid", 5*time.Millisecond, false, func() { fired <- struct{}{} })
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("dynamic wakeup never fired")
	}
	// No second tick without an explicit re-arm.
	select {
	case <-fired:
		t.Fatal("dynamic wakeup must not re-arm itself")
	case <-time.After(50 * time.Millisecond):
	}
	if n := s.armedCount(); n != 0 {
		t.Fatalf("a spent dynamic timer should leave nothing armed, got %d", n)
	}
}

// clearWakeupClockIfIdle reclaims an abandoned dynamic loop's clock but keeps an
// interval loop's (whose timer is still armed).
func TestServerWaker_ClockReclaimedWhenIdle(t *testing.T) {
	s := newLoopTestServer()
	// Abandoned dynamic loop: clock set, no timer.
	s.wakeupStart["sid"] = time.Now()
	s.clearWakeupClockIfIdle("sid")
	if _, ok := s.wakeupStart["sid"]; ok {
		t.Fatal("an idle (no-timer) clock should be reclaimed")
	}
	// Active loop: timer armed → clock kept.
	s.armWakeup("sid2", time.Hour, "p", true)
	s.clearWakeupClockIfIdle("sid2")
	if _, ok := s.wakeupStart["sid2"]; !ok {
		t.Fatal("an armed loop's clock must not be reclaimed")
	}
	s.cancelWakeup("sid2")
}

// stopAllWakeups (shutdown) tears down every armed loop and clock.
func TestServerWaker_StopAll(t *testing.T) {
	s := newLoopTestServer()
	s.armWakeup("a", time.Hour, "p", true)
	s.armWakeup("b", time.Hour, "p", false)
	s.stopAllWakeups()
	if n := s.armedCount(); n != 0 {
		t.Fatalf("stopAllWakeups should clear timers, got %d", n)
	}
	if len(s.wakeupStart) != 0 {
		t.Fatalf("stopAllWakeups should clear clocks, got %d", len(s.wakeupStart))
	}
}

func newLoopTestServer() *Server {
	return &Server{
		wakeupTimers: map[string]*time.Timer{},
		wakeupStart:  map[string]time.Time{},
	}
}

func (s *Server) armedCount() int {
	s.wakeupMu.Lock()
	defer s.wakeupMu.Unlock()
	return len(s.wakeupTimers)
}
