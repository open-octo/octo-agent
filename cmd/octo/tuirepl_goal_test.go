package main

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/open-octo/octo-agent/internal/agent"
)

// newGoalTestModel builds a TUI model with goals wired: the session is both
// the store and the accountant, exactly like chat.go's TUI path.
func newGoalTestModel() (*tuiModel, *agent.Session) {
	isolateTestInputHistory()
	sess := agent.NewSession("m", "")
	a := agent.New(&stubSender{reply: "ok"}, "m")
	a.GoalAcct = sess
	m := newTUIModel(replConfig{a: a, session: sess, noSave: true})
	m.sink = &tuiSink{prog: &fakeProg{}}
	return m, sess
}

func printed(m *tuiModel) string { return strings.Join(m.printlnBuf, "\n") }

func TestDispatchGoal_CreateSummaryAndGuardedReplace(t *testing.T) {
	m, sess := newGoalTestModel()

	m.dispatchGoal("ship the release")
	g, ok := sess.GoalSnapshot()
	if !ok || g.Status != agent.GoalActive || g.Objective != "ship the release" {
		t.Fatalf("create failed: %+v", g)
	}

	// Bare /goal shows the summary.
	m.printlnBuf = nil
	m.dispatchGoal("")
	if out := printed(m); !strings.Contains(out, "ship the release") || !strings.Contains(out, "active") {
		t.Errorf("summary missing fields:\n%s", out)
	}

	// A new objective over an unfinished goal is refused with a hint.
	m.printlnBuf = nil
	m.dispatchGoal("something else")
	if g2, _ := sess.GoalSnapshot(); g2.Objective != "ship the release" {
		t.Error("unfinished goal must not be silently replaced")
	}
	if !strings.Contains(printed(m), "/goal replace") {
		t.Errorf("refusal must hint at /goal replace:\n%s", printed(m))
	}

	// Explicit replace mints a fresh goal.
	m.dispatchGoal("replace something else")
	if g3, _ := sess.GoalSnapshot(); g3.Objective != "something else" || g3.ID == g.ID {
		t.Errorf("replace failed: %+v", g3)
	}

	// A complete goal is replaced without ceremony.
	if _, err := sess.SetGoalStatus(agent.GoalComplete); err != nil {
		t.Fatal(err)
	}
	m.dispatchGoal("next objective")
	if g4, _ := sess.GoalSnapshot(); g4.Objective != "next objective" || g4.Status != agent.GoalActive {
		t.Errorf("complete goal should be replaced silently: %+v", g4)
	}
}

func TestDispatchGoal_PauseResumeClear(t *testing.T) {
	m, sess := newGoalTestModel()
	m.dispatchGoal("g")

	m.dispatchGoal("pause")
	if g, _ := sess.GoalSnapshot(); g.Status != agent.GoalPaused {
		t.Errorf("pause: %+v", g)
	}

	// Resume re-activates AND kicks the continuation loop immediately.
	m.dispatchGoal("resume")
	if g, _ := sess.GoalSnapshot(); g.Status != agent.GoalActive {
		t.Errorf("resume: %+v", g)
	}
	if !m.turnRunning {
		t.Error("resume should kick an idle continuation turn")
	}

	m.turnRunning = false
	m.dispatchGoal("clear")
	if _, ok := sess.GoalSnapshot(); ok {
		t.Error("clear left a goal behind")
	}
}

func TestDispatchGoal_UnwiredIsDisabled(t *testing.T) {
	m := newTestModel() // no GoalAcct, no session
	if _, cmd := m.dispatchGoal("anything"); cmd != nil {
		t.Error("unwired /goal must not start anything")
	}
	if !strings.Contains(printed(m), "disabled") {
		t.Errorf("unwired /goal should say so:\n%s", printed(m))
	}
}

func TestGoalEditFlow(t *testing.T) {
	m, sess := newGoalTestModel()
	m.dispatchGoal("first objective")
	sess.AccountGoalUsage(0) // consume the mid-turn-creation skip deterministically
	sess.ResetGoalWallClock()
	sess.AccountGoalUsage(42)

	m.dispatchGoal("edit")
	if !m.goalEditPending || m.ta.Value() != "first objective" {
		t.Fatalf("edit should arm and prefill, pending=%v input=%q", m.goalEditPending, m.ta.Value())
	}

	// The next submit is the edited objective; counters survive.
	setInput(m, "revised objective")
	m.submit()
	if m.goalEditPending {
		t.Error("submit should consume the edit")
	}
	g, _ := sess.GoalSnapshot()
	if g.Objective != "revised objective" || g.TokensUsed != 42 {
		t.Errorf("edit lost state: %+v", g)
	}
}

func TestGoalEdit_EmptySubmitCancels(t *testing.T) {
	m, sess := newGoalTestModel()
	m.dispatchGoal("keep me")
	m.dispatchGoal("edit")
	m.ta.Reset()
	m.submit()
	if m.goalEditPending {
		t.Error("clearing the prefill and pressing Enter must cancel the edit")
	}
	if g, _ := sess.GoalSnapshot(); g.Objective != "keep me" {
		t.Errorf("cancelled edit must not change the objective: %+v", g)
	}
}

func TestGoalEdit_EscCancelsViaKeyHandler(t *testing.T) {
	m, sess := newGoalTestModel()
	m.dispatchGoal("keep me")
	m.dispatchGoal("edit")

	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.goalEditPending {
		t.Error("Esc must cancel a pending goal edit")
	}
	// The next submit is an ordinary message again, not an objective.
	setInput(m, "unrelated message")
	m.submit()
	if g, _ := sess.GoalSnapshot(); g.Objective != "keep me" {
		t.Errorf("post-cancel submit must not touch the goal: %+v", g)
	}
}

func TestGoalEdit_SlashCommandCancelsAndDispatches(t *testing.T) {
	m, sess := newGoalTestModel()
	m.dispatchGoal("keep me")
	m.dispatchGoal("edit")

	// The user changed their mind mid-edit and typed a command instead.
	setInput(m, "/goal")
	m.submit()
	if m.goalEditPending {
		t.Error("a slash command must cancel the pending edit")
	}
	if g, _ := sess.GoalSnapshot(); g.Objective != "keep me" {
		t.Errorf("the command text must not become the objective: %+v", g)
	}
	if !strings.Contains(printed(m), "keep me") {
		t.Errorf("the /goal command should still have dispatched (summary):\n%s", printed(m))
	}
}

func TestGoalEdit_AsyncTurnStartCancelsPendingEdit(t *testing.T) {
	// Regression: an async idle auto-turn (background exit note, loop wakeup)
	// starting while /goal edit is armed must cancel the edit — otherwise the
	// user's next unrelated message is silently consumed as the objective.
	m, sess := newGoalTestModel()
	m.dispatchGoal("keep me")
	m.dispatchGoal("edit")

	m.startTurnEcho("background note turn", "")
	if m.goalEditPending {
		t.Fatal("a turn start must cancel the pending goal edit")
	}

	// While that auto-turn runs, the user's text goes out as an ordinary
	// steer — not as the objective (the exact leak this guards against).
	setInput(m, "unrelated message")
	m.submit()
	if g, _ := sess.GoalSnapshot(); g.Objective != "keep me" {
		t.Errorf("mid-auto-turn submit must not become the objective: %+v", g)
	}
	if len(m.pendingSteer) != 1 || m.pendingSteer[0] != "unrelated message" {
		t.Errorf("the text should have routed to the steer path, got %v", m.pendingSteer)
	}
}

func TestHandleTurnFinished_KicksGoalContinuation(t *testing.T) {
	m, _ := newGoalTestModel()
	m.dispatchGoal("keep going")
	m.turnRunning = false

	m.handleTurnFinished(nil)
	if !m.turnRunning {
		t.Fatal("idle turn end with an active goal should start a continuation turn")
	}
	// The "Goal continues" notice was already flushed into the returned
	// tea.Sequence (flushPrints drains the buffer), so the buffer is empty
	// here — the kick itself (turnRunning) is the observable effect.
}

func TestHandleTurnFinished_QueuedInputBeatsContinuation(t *testing.T) {
	m, _ := newGoalTestModel()
	m.dispatchGoal("keep going")
	m.turnRunning = false
	m.queue = append(m.queue, pendingItem{text: "user question"})

	m.handleTurnFinished(nil)
	if strings.Contains(printed(m), "Goal continues") {
		t.Error("queued user input must run before any continuation")
	}
}

func TestHandleTurnFinished_ErrorParksContinuation(t *testing.T) {
	m, sess := newGoalTestModel()
	m.dispatchGoal("keep going")
	m.turnRunning = false

	m.handleTurnFinished(errors.New("anthropic: HTTP 500: overloaded"))
	if m.turnRunning {
		t.Fatal("an errored turn must not chain a continuation")
	}
	if _, ok := sess.GoalContinuation(); ok {
		t.Error("continuation must stay suppressed after an errored turn")
	}
	if g, _ := sess.GoalSnapshot(); g.Status != agent.GoalActive {
		t.Errorf("plain errors must not change goal status: %+v", g)
	}
}

func TestHandleTurnFinished_RateLimitedContinuationParks(t *testing.T) {
	m, sess := newGoalTestModel()
	m.dispatchGoal("keep going")
	m.turnRunning = false

	// Hand out a continuation (pending), then its turn fails on a rate limit.
	if _, ok := sess.GoalContinuation(); !ok {
		t.Fatal("precondition: continuation fires")
	}
	m.handleTurnFinished(errors.New("openai: HTTP 429: rate limited"))
	if g, _ := sess.GoalSnapshot(); g.Status != agent.GoalUsageLimited {
		t.Errorf("rate-limited continuation should park usage_limited: %+v", g)
	}
}

func TestGoalStatusSegmentAndStartupNotice(t *testing.T) {
	seg := goalStatusSegment(agent.Goal{Status: agent.GoalActive, TokenBudget: 50000, TokensUsed: 12500})
	if seg != "12.5K/50K" {
		t.Errorf("budgeted active segment = %q", seg)
	}
	seg = goalStatusSegment(agent.Goal{Status: agent.GoalActive, TimeUsedSeconds: 5400})
	if seg != "1h 30m" {
		t.Errorf("unbudgeted active segment = %q", seg)
	}
	if seg = goalStatusSegment(agent.Goal{Status: agent.GoalPaused}); seg != "paused" {
		t.Errorf("paused segment = %q", seg)
	}

	sess := agent.NewSession("m", "")
	if goalStartupNotice(sess) != "" {
		t.Error("no goal → no startup notice")
	}
	if _, err := sess.CreateGoal("obj", 0); err != nil {
		t.Fatal(err)
	}
	if n := goalStartupNotice(sess); !strings.Contains(n, "active") {
		t.Errorf("active goal notice = %q", n)
	}
	if _, err := sess.SetGoalStatus(agent.GoalPaused); err != nil {
		t.Fatal(err)
	}
	if n := goalStartupNotice(sess); !strings.Contains(n, "/goal resume") {
		t.Errorf("paused goal notice should hint resume, got %q", n)
	}
}

func TestGoalStatusBar_IncludesGoalSegment(t *testing.T) {
	// StatusBar renders segment VALUES only (labels pick styles), so assert
	// on a value no other segment can produce: the budgeted-goal usage.
	m, sess := newGoalTestModel()
	if _, err := sess.CreateGoal("visible goal", 50000); err != nil {
		t.Fatal(err)
	}
	if out := m.renderStatusBar(); !strings.Contains(out, "0/50K") {
		t.Errorf("status bar should carry the goal usage segment:\n%s", out)
	}
}

func TestCompactCountAndElapsed(t *testing.T) {
	for n, want := range map[int64]string{950: "950", 1200: "1.2K", 50000: "50K", 1_500_000: "1.5M"} {
		if got := compactCount(n); got != want {
			t.Errorf("compactCount(%d) = %q, want %q", n, got, want)
		}
	}
	for s, want := range map[int64]string{45: "45s", 720: "12m", 5400: "1h 30m", 7200: "2h", 183900: "2d 3h 5m"} {
		if got := goalElapsed(s); got != want {
			t.Errorf("goalElapsed(%d) = %q, want %q", s, got, want)
		}
	}
}
