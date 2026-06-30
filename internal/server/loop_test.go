package server

import (
	"testing"
	"time"

	"github.com/Leihb/octo-agent/internal/tools"
)

// Long delays keep the timer from firing during the test, so we exercise the
// arm/cancel bookkeeping without the live-session delivery path.

func TestServerWaker_ArmAndCancel(t *testing.T) {
	s := &Server{wakeupTimers: map[string]*time.Timer{}}
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
	s := &Server{wakeupTimers: map[string]*time.Timer{}}
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
	s := &Server{wakeupTimers: map[string]*time.Timer{}}
	var w tools.Waker = s.wakerFor("sid")
	if err := w.ScheduleWakeup(time.Hour, "p", "r", false); err != nil {
		t.Fatalf("ScheduleWakeup: %v", err)
	}
	if n := s.armedCount(); n != 1 {
		t.Fatalf("ScheduleWakeup should arm a timer, got %d", n)
	}
	s.cancelWakeup("sid")
}

func (s *Server) armedCount() int {
	s.wakeupMu.Lock()
	defer s.wakeupMu.Unlock()
	return len(s.wakeupTimers)
}
