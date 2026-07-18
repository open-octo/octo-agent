package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// wakeupCaptureSender records the message list of the first turn it serves,
// so a test can inspect the exact user message a fired tick ran with.
type wakeupCaptureSender struct {
	msgs chan []agent.Message
}

func (s *wakeupCaptureSender) SendMessages(_ context.Context, _, _ string, msgs []agent.Message, _ int) (agent.Reply, error) {
	s.msgs <- msgs
	return agent.Reply{Content: "ok"}, nil
}

func (s *wakeupCaptureSender) StreamMessages(_ context.Context, _, _ string, msgs []agent.Message, _ int, _ func(string), _ func(string)) (agent.Reply, error) {
	s.msgs <- msgs
	return agent.Reply{Content: "ok"}, nil
}

// armWakeupMsg (posted by the schedule_wakeup tool) arms the loop timer.
func TestTUI_ArmWakeup(t *testing.T) {
	m := newTestModel()
	m.Update(armWakeupMsg{delay: time.Hour, prompt: "tick", repeat: true})
	if !m.loopActive || m.wakeupTimer == nil {
		t.Fatal("armWakeupMsg should arm the loop")
	}
	m.cancelWakeup()
}

// An armed wakeup that fires while idle starts a fresh turn; dynamic mode
// (repeat=false) clears the loop so the model must re-arm to continue.
func TestTUI_WakeupIdleStartsTurn(t *testing.T) {
	m := newTestModel()
	m.armWakeup(time.Hour, "tick", false)
	m.Update(wakeupMsg{prompt: "tick", repeat: false, delay: time.Hour})
	if !m.turnRunning {
		t.Fatal("an idle wakeup should start a turn")
	}
	if m.loopActive {
		t.Error("dynamic-mode wakeup should clear loopActive after firing")
	}
}

// A wakeup that fires while the model is still busy must not start a second
// turn; interval mode re-arms instead.
func TestTUI_WakeupBusyReArms(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	m.armWakeup(time.Hour, "tick", true)
	m.Update(wakeupMsg{prompt: "tick", repeat: true, delay: time.Hour})
	if !m.loopActive || m.wakeupTimer == nil {
		t.Error("a repeat wakeup arriving while busy should re-arm, not lapse")
	}
	m.cancelWakeup()
}

// A new user message does NOT cancel the loop — they coexist (CC-style).
func TestTUI_UserMessageKeepsLoop(t *testing.T) {
	m := newTestModel()
	m.armWakeup(time.Hour, "tick", true)
	setInput(m, "do something else")
	_, _ = m.submit()
	if !m.loopActive || m.wakeupTimer == nil {
		t.Fatal("a user message must not cancel the armed loop (they coexist)")
	}
	m.cancelWakeup()
}

// A fired tick runs the prompt wrapped as a <system-reminder>
// (tools.FormatLoopTick) — parity with the web and IM paths. The TUI itself
// only prints "● Loop tick", and the wrapped user message is what persists, so
// a web/desktop UI replaying this transcript derives empty visible text and
// can't render the tick as a fake user bubble. The model still receives the
// task verbatim.
func TestTUI_WakeupTickWrapsPrompt(t *testing.T) {
	isolateTestInputHistory()
	cs := &wakeupCaptureSender{msgs: make(chan []agent.Message, 1)}
	a := agent.New(cs, "m")
	m := newTUIModel(replConfig{a: a, noSave: true})
	m.sink = &tuiSink{prog: &fakeProg{}}
	m.armWakeup(time.Hour, "check the PR", false)
	m.Update(wakeupMsg{prompt: "check the PR", repeat: false, delay: time.Hour})

	select {
	case msgs := <-cs.msgs:
		last := msgs[len(msgs)-1]
		if last.Role != "user" {
			t.Fatalf("last message role = %q, want user", last.Role)
		}
		if !strings.Contains(last.Content, "check the PR") {
			t.Fatalf("tick prompt must reach the model verbatim, got %q", last.Content)
		}
		if !strings.Contains(last.Content, "[LOOP TICK]") {
			t.Fatalf("tick prompt must be wrapped as a loop tick, got %q", last.Content)
		}
		if visible := strings.TrimSpace(agent.StripSystemReminders(last.Content)); visible != "" {
			t.Fatalf("wrapped tick must leave no visible user-bubble text, got %q", visible)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the wakeup turn never reached the model")
	}
}

// schedule_wakeup(cancel=true) → cancelWakeupMsg stops the loop.
func TestTUI_CancelWakeupMsgStopsLoop(t *testing.T) {
	m := newTestModel()
	m.armWakeup(time.Hour, "tick", true)
	m.Update(cancelWakeupMsg{})
	if m.loopActive || m.wakeupTimer != nil {
		t.Fatal("cancelWakeupMsg should stop the loop")
	}
}

// Anti-leak: a loop past tools.MaxLoopLifetime stops on its next re-arm.
func TestTUI_MaxLifetimeStops(t *testing.T) {
	m := newTestModel()
	m.armWakeup(time.Hour, "tick", true)
	m.loopStart = time.Now().Add(-2 * tools.MaxLoopLifetime) // simulate a long-running loop
	m.armWakeup(time.Hour, "tick", true)                     // next tick's re-arm
	if m.loopActive || m.wakeupTimer != nil {
		t.Fatal("an expired loop must stop instead of re-arming")
	}
}

// A dynamic tick keeps the loop clock so its lifetime accumulates across ticks.
func TestTUI_DynamicTickKeepsClock(t *testing.T) {
	m := newTestModel()
	m.armWakeup(time.Hour, "tick", false)
	start := m.loopStart
	if start.IsZero() {
		t.Fatal("arming should stamp the loop clock")
	}
	m.wakeupFired() // a dynamic tick fired
	if m.loopStart != start {
		t.Fatal("a dynamic tick must keep the loop clock, not reset it")
	}
	m.cancelWakeup()
	if !m.loopStart.IsZero() {
		t.Fatal("cancel should reset the loop clock")
	}
}

// An interrupt (Ctrl+C) is the hard manual stop.
func TestTUI_InterruptCancelsLoop(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	m.cancelTurn = func() {}
	m.armWakeup(time.Hour, "tick", true)
	m.interrupt()
	if m.loopActive || m.wakeupTimer != nil {
		t.Fatal("an interrupt should cancel the armed loop")
	}
}
