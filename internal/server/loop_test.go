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
