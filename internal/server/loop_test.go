package server

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/channel"
	"github.com/open-octo/octo-agent/internal/tools"
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

// An interval tick must not enqueue a steer while a turn is already running: the
// steer would sit undrained (kickIdleSteerTurn bails when busy) and every later
// tick would stack another identical copy. The re-armed timer delivers on the
// next idle tick instead.
func TestDeliverLoopTick_IntervalSkipsWhileTurnRunning(t *testing.T) {
	s := newLoopTestServer()
	s.turnRunning["sid"] = true
	s.deliverLoopTick("sid", "check whether the PR is merged", true)
	if q := s.steerQueues["sid"]; len(q) != 0 {
		t.Fatalf("expected no steer enqueued while a turn runs, got %d", len(q))
	}
}

// shouldSkipTick gates only interval ticks on a running turn. A dynamic tick
// (repeat=false) fires once and never re-arms, so it must deliver even while a
// turn runs — skipping it would silently kill the loop.
func TestShouldSkipTick(t *testing.T) {
	cases := []struct {
		name    string
		repeat  bool
		running bool
		skip    bool
	}{
		{"interval, turn running", true, true, true},
		{"interval, idle", true, false, false},
		{"dynamic, turn running", false, true, false}, // must NOT skip: no re-arm
		{"dynamic, idle", false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newLoopTestServer()
			s.turnRunning["sid"] = tc.running
			if got := s.shouldSkipTick("sid", tc.repeat); got != tc.skip {
				t.Fatalf("shouldSkipTick(repeat=%v, running=%v) = %v, want %v", tc.repeat, tc.running, got, tc.skip)
			}
		})
	}
}

// A loop tick is wrapped as a <system-reminder> so the web transcript stops
// duplicating the prompt as a user-message bubble on every tick. The model must
// still receive the prompt verbatim, and the visible bubble text (what
// doAgentTurn derives via StripSystemReminders) must be empty — the same
// suppression that keeps completion notes from rendering as user speech.
func TestFormatLoopTick_SuppressesUserBubble(t *testing.T) {
	prompt := "check whether the PR is merged and report"
	wrapped := formatLoopTick(prompt)

	if !strings.Contains(wrapped, prompt) {
		t.Fatalf("wrapped tick must carry the original task verbatim, got %q", wrapped)
	}
	if visible := strings.TrimSpace(agent.StripSystemReminders(wrapped)); visible != "" {
		t.Fatalf("a loop tick must leave no visible user-bubble text, got %q", visible)
	}
}

func TestServer_TurnActive(t *testing.T) {
	s := newLoopTestServer()
	if s.turnActive("sid") {
		t.Fatal("a session with no running turn must report inactive")
	}
	s.turnRunning["sid"] = true
	if !s.turnActive("sid") {
		t.Fatal("a session with a running turn must report active")
	}
}

func newLoopTestServer() *Server {
	return &Server{
		wakeupTimers: map[string]*time.Timer{},
		wakeupStart:  map[string]time.Time{},
		turnRunning:  map[string]bool{},
		turnLocks:    map[string]*sync.Mutex{},
		steerQueues:  map[string][]agent.InboxItem{},
	}
}

func (s *Server) armedCount() int {
	s.wakeupMu.Lock()
	defer s.wakeupMu.Unlock()
	return len(s.wakeupTimers)
}
