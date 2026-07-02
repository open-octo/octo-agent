package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/channel"
)

// goalRoundSender replies (or errs) per main-loop round, recording each call's
// message snapshot. Mutex-guarded — the after-turn suggest goroutine calls the
// sender concurrently. Rounds beyond the script error out as a fail-safe so a
// broken continuation guard can't hang the test in an infinite chain.
type goalRoundSender struct {
	mu     sync.Mutex
	rounds int
	calls  [][]agent.Message
	errAt  int   // 1-based round that errors; 0 = never
	err    error // the error to return at errAt
	usage  int   // InputTokens per successful reply
}

func (s *goalRoundSender) SendMessages(_ context.Context, _, _ string, msgs []agent.Message, _ int) (agent.Reply, error) {
	// The after-turn suggest/title goroutines share this sender; their
	// side-calls must not consume scripted rounds. Both end on a distinctive
	// instruction user message.
	if n := len(msgs); n > 0 {
		last := msgs[n-1].Content
		if strings.Contains(last, "Suggest ONE concise") || strings.Contains(last, "Summarize this conversation") {
			return agent.Reply{Content: "side-call stub"}, nil
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rounds++
	s.calls = append(s.calls, append([]agent.Message(nil), msgs...))
	if s.errAt != 0 && s.rounds == s.errAt {
		return agent.Reply{}, s.err
	}
	if s.rounds > 6 {
		return agent.Reply{}, errors.New("test fail-safe: continuation chain did not terminate")
	}
	return agent.Reply{Content: "stub reply", InputTokens: s.usage}, nil
}

func goalTestServer(t *testing.T) (*Server, *agent.Session) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initWS()
	srv.goalsEnabled = true
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	return srv, sess
}

// countGoalContextUserMsgs counts user messages carrying a <goal_context>
// span in a message list.
func countGoalContextUserMsgs(msgs []agent.Message) int {
	n := 0
	for _, m := range msgs {
		text := m.Content
		if text == "" {
			for _, b := range m.Blocks {
				if b.Type == "text" {
					text += b.Text
				}
			}
		}
		if m.Role == agent.RoleUser && strings.Contains(text, "<goal_context>") {
			n++
		}
	}
	return n
}

func TestRunAgentTurnLoop_ChainsGoalContinuationOnce(t *testing.T) {
	// An active goal chains one continuation turn after the user turn; the
	// continuation turn accounts zero tokens (stub usage 0), so the
	// zero-progress audit suppresses a second one and the loop exits.
	srv, sess := goalTestServer(t)
	if _, err := sess.CreateGoal("keep going", 0); err != nil {
		t.Fatal(err)
	}
	sender := &goalRoundSender{}
	srv.sender = sender

	srv.turnRunning[sess.ID] = true
	srv.runAgentTurnLoop(sess, "start", nil, nil)

	sender.mu.Lock()
	defer sender.mu.Unlock()
	// The continuation turn's LLM call must have seen the hidden prompt.
	sawContinuation := 0
	for _, call := range sender.calls {
		sawContinuation += countGoalContextUserMsgs(call)
	}
	if sawContinuation == 0 {
		t.Fatal("no LLM call carried the goal continuation prompt — the loop never chained")
	}
	// Exactly one continuation user message exists across the final history:
	// the zero-progress guard must stop the chain after the first idle turn.
	last := sender.calls[len(sender.calls)-1]
	if got := countGoalContextUserMsgs(last); got != 1 {
		t.Errorf("continuation prompts in final history = %d, want exactly 1 (zero-progress guard)", got)
	}
}

func TestRunAgentTurnLoop_RateLimitedContinuationParksUsageLimited(t *testing.T) {
	// Round 1 = user turn (ok), round 2 = continuation turn failing with a
	// rate-limit error → the goal parks as usage_limited and the chain stops.
	srv, sess := goalTestServer(t)
	if _, err := sess.CreateGoal("keep going", 0); err != nil {
		t.Fatal(err)
	}
	srv.sender = &goalRoundSender{errAt: 2, err: errors.New("anthropic: HTTP 429: rate limited")}

	srv.turnRunning[sess.ID] = true
	srv.runAgentTurnLoop(sess, "start", nil, nil)

	g, ok := sess.GoalSnapshot()
	if !ok || g.Status != agent.GoalUsageLimited {
		t.Errorf("goal after rate-limited continuation = %+v, want usage_limited", g)
	}
}

func TestRunAgentTurnLoop_ErroredTurnSuppressesContinuation(t *testing.T) {
	// A persistent non-rate-limit provider error must not be retried by the
	// idle loop: the error parks continuation (until user activity) and the
	// goal stays active for a later resume.
	srv, sess := goalTestServer(t)
	if _, err := sess.CreateGoal("keep going", 0); err != nil {
		t.Fatal(err)
	}
	sender := &goalRoundSender{errAt: 2, err: errors.New("anthropic: HTTP 400: broken history shape")}
	srv.sender = sender

	srv.turnRunning[sess.ID] = true
	srv.runAgentTurnLoop(sess, "start", nil, nil)

	sender.mu.Lock()
	rounds := sender.rounds
	sender.mu.Unlock()
	if rounds > 2 {
		t.Errorf("errored continuation must not chain again, got %d rounds", rounds)
	}
	g, _ := sess.GoalSnapshot()
	if g.Status != agent.GoalActive {
		t.Errorf("a plain provider error must not change goal status, got %q", g.Status)
	}
	if _, ok := sess.GoalContinuation(); ok {
		t.Error("continuation must stay suppressed after an errored turn")
	}
}

func TestSteerPending(t *testing.T) {
	s := &Server{steerQueues: make(map[string][]agent.InboxItem)}
	if s.steerPending("sid") {
		t.Error("empty queue should not be pending")
	}
	s.enqueueSteer("sid", agent.InboxItem{Text: "hello"})
	if !s.steerPending("sid") {
		t.Error("queued steer should be pending")
	}
	s.drainSteer("sid")
	if s.steerPending("sid") {
		t.Error("drained queue should not be pending")
	}
}

func TestGoalRESTEndpoints(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := agent.NewSession("stub-model", "")
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.goalsEnabled = true

	do := func(method, path, body string) *httptest.ResponseRecorder {
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, rd)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		serveLoopback(srv.mux, w, req)
		return w
	}
	goalPath := "/api/sessions/" + sess.ID + "/goal"

	// GET with no goal → null.
	w := do(http.MethodGet, goalPath, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"goal":null`) {
		t.Fatalf("GET empty: %d %s", w.Code, w.Body.String())
	}

	// PUT objective creates.
	w = do(http.MethodPut, goalPath, `{"objective":"ship it","token_budget":50000}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"active"`) {
		t.Fatalf("PUT create: %d %s", w.Code, w.Body.String())
	}

	// PUT objective again edits in place (counters survive; same via reload).
	w = do(http.MethodPut, goalPath, `{"objective":"ship it properly"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT edit: %d %s", w.Code, w.Body.String())
	}

	// PUT status pause / resume; model-owned statuses rejected.
	if w = do(http.MethodPut, goalPath, `{"status":"paused"}`); w.Code != http.StatusOK {
		t.Fatalf("PUT pause: %d %s", w.Code, w.Body.String())
	}
	if w = do(http.MethodPut, goalPath, `{"status":"complete"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("PUT complete must be rejected (model-owned): %d", w.Code)
	}

	// State persisted: reload from disk.
	got, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g, ok := got.GoalSnapshot(); !ok || g.Objective != "ship it properly" || g.Status != agent.GoalPaused {
		t.Fatalf("persisted goal = %+v", g)
	}

	// Replace mints fresh.
	if w = do(http.MethodPut, goalPath, `{"objective":"round two","replace":true}`); w.Code != http.StatusOK {
		t.Fatalf("PUT replace: %d %s", w.Code, w.Body.String())
	}

	// DELETE clears; second delete reports cleared=false.
	if w = do(http.MethodDelete, goalPath, ""); !strings.Contains(w.Body.String(), `"cleared":true`) {
		t.Fatalf("DELETE: %d %s", w.Code, w.Body.String())
	}
	if w = do(http.MethodDelete, goalPath, ""); !strings.Contains(w.Body.String(), `"cleared":false`) {
		t.Fatalf("re-DELETE: %d %s", w.Code, w.Body.String())
	}

	// Disabled server rejects mutations.
	srv.goalsEnabled = false
	if w = do(http.MethodPut, goalPath, `{"objective":"x"}`); w.Code != http.StatusForbidden {
		t.Fatalf("disabled PUT: %d", w.Code)
	}
}

func TestGoalSession_PrefersLiveSession(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := agent.NewSession("stub-model", "")
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	// No live turn: a fresh load (different pointer, same ID).
	got, err := srv.goalSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == sess {
		t.Fatal("expected a fresh load when no turn is running")
	}

	// With a registered live session, mutations must target it.
	srv.sessionAgentsMu.Lock()
	srv.liveSessions = map[string]*agent.Session{sess.ID: sess}
	srv.sessionAgentsMu.Unlock()
	got, err = srv.goalSession(sess.ID)
	if err != nil || got != sess {
		t.Fatalf("expected the live session object, got %p err=%v", got, err)
	}
}

// usageStubSender is stubSender plus token usage, so goal accounting sees
// real deltas (a budget crossing needs tokens to bill).
type usageStubSender struct {
	mu     sync.Mutex
	rounds int
}

func (s *usageStubSender) SendMessages(_ context.Context, _, _ string, _ []agent.Message, _ int) (agent.Reply, error) {
	s.mu.Lock()
	s.rounds++
	s.mu.Unlock()
	return agent.Reply{Content: "im stub", StopReason: "end_turn", InputTokens: 100, OutputTokens: 20}, nil
}

func TestChannelTurn_GoalContinuationAndZeroProgressStop(t *testing.T) {
	// An active goal chains hidden continuation turns after the IM turn; the
	// zero-usage stub means the continuation accounts nothing, so the
	// zero-progress guard stops the chain after one hidden turn.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	srv := chanServer(t)
	srv.goalsEnabled = true
	ad := &fullFakeAdapter{}

	sess := srv.channelMgr.GetOrCreateSession(evFor("x"))
	if _, err := sess.GoalStore().CreateGoal("keep going", 0); err != nil {
		t.Fatal(err)
	}

	srv.handleChannelMessage(context.Background(), ad, evFor("start"))

	msgs := sess.Agent.History.Snapshot()
	cont := 0
	for _, m := range msgs {
		text := m.Content
		if text == "" {
			for _, b := range m.Blocks {
				if b.Type == "text" {
					text += b.Text
				}
			}
		}
		if m.Role == agent.RoleUser && strings.Contains(text, "<goal_context>") {
			cont++
		}
	}
	if cont != 1 {
		t.Errorf("continuation prompts in history = %d, want exactly 1 (zero-progress stop)", cont)
	}
	// The goal stays active; no terminal-transition notice was sent.
	for _, txt := range ad.texts() {
		if strings.Contains(txt, "Goal") {
			t.Errorf("no goal notice expected, got %q", txt)
		}
	}
}

func TestChannelTurn_BudgetCrossingSendsNotice(t *testing.T) {
	// A budgeted goal crossing its budget during the chain sends the
	// budget-reached chat notice (IM users aren't watching a status line).
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	srv := chanServer(t)
	srv.goalsEnabled = true
	// Replace the factory-made zero-usage agent with one that reports usage.
	srv.channelMgr = channel.NewManager(&channel.Config{}, func() *agent.Agent {
		return agent.New(&usageStubSender{}, "stub-model")
	}, channel.BindByChat)
	ad := &fullFakeAdapter{}

	sess := srv.channelMgr.GetOrCreateSession(evFor("x"))
	if _, err := sess.GoalStore().CreateGoal("small budget", 50); err != nil {
		t.Fatal(err)
	}
	// Consume the mid-turn-creation skip so the first turn's usage bills.
	sess.GoalStore().ResetGoalWallClock()

	srv.handleChannelMessage(context.Background(), ad, evFor("start"))

	if g, _ := sess.GoalStore().GoalSnapshot(); g.Status != agent.GoalBudgetLimited {
		t.Fatalf("goal should be budget_limited, got %+v", g)
	}
	found := false
	for _, txt := range ad.texts() {
		if strings.Contains(txt, "Goal budget reached") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a budget-reached notice, got %v", ad.texts())
	}
}
