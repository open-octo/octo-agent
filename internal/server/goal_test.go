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
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/agentprofile"
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
	srv.goalsEnabled.Store(true)
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]queuedTurn)
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

// waitGoalTurnIdle blocks until the session has no turn running. The kick sets
// turnRunning synchronously before returning, so a caller that has already
// issued the command sees a true reading before this ever observes false.
func waitGoalTurnIdle(t *testing.T, srv *Server, sessionID string) {
	t.Helper()
	mu := srv.sessionTurnLock(sessionID)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		running := srv.turnRunning[sessionID]
		mu.Unlock()
		if !running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("turn never wound down")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A goal set from the web composer must start pursuing itself immediately, the
// way the TUI's /goal does. Web used to only continue a goal at the tail of a
// turn already running, so a goal created while idle sat active and untouched
// until the user happened to send an unrelated message.
func TestWSGoalCommand_StartsIdleTurn(t *testing.T) {
	srv, sess := goalTestServer(t)
	sender := &goalRoundSender{}
	srv.sender = sender

	srv.wsGoalCommand(sess.ID, "ship the release")
	waitGoalTurnIdle(t, srv, sess.ID)

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.calls) == 0 {
		t.Fatal("/goal <objective> started no turn")
	}
	saw := 0
	for _, call := range sender.calls {
		saw += countGoalContextUserMsgs(call)
	}
	if saw == 0 {
		t.Error("the turn that started did not carry the goal continuation prompt")
	}
}

// Pause must not start work; the resume that follows must. Both run against a
// reloaded session — wsGoalCommand mutates whatever copy is authoritative, so
// the pause has to be durable for the resume to see it.
func TestWSGoalCommand_PauseIdleResume(t *testing.T) {
	srv, sess := goalTestServer(t)
	if _, err := sess.CreateGoal("keep going", 0); err != nil {
		t.Fatal(err)
	}
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	sender := &goalRoundSender{}
	srv.sender = sender

	srv.wsGoalCommand(sess.ID, "pause")
	waitGoalTurnIdle(t, srv, sess.ID)
	sender.mu.Lock()
	calls := len(sender.calls)
	sender.mu.Unlock()
	if calls != 0 {
		t.Fatalf("/goal pause started %d LLM call(s), want none", calls)
	}

	srv.wsGoalCommand(sess.ID, "resume")
	waitGoalTurnIdle(t, srv, sess.ID)
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.calls) == 0 {
		t.Error("/goal resume started no turn")
	}
}

// waitEventType reads broadcasts off conn until one of the wanted type shows
// up, skipping the unrelated events (goal_updated, context usage) that share
// the stream.
func waitEventType(t *testing.T, conn *wsConn, typ string) map[string]any {
	t.Helper()
	for i := 0; i < 32; i++ {
		if ev := nextEvent(t, conn); ev["type"] == typ {
			return ev
		}
	}
	t.Fatalf("no %s event in the first 32 broadcasts", typ)
	return nil
}

// The web's answer to the TUI's "● Goal …" scrollback: the command's reply
// lands in the transcript, and the continuation turn it kicks announces itself
// — without that line the turn's output would read as unprompted, since the
// continuation prompt is hidden.
func TestWSGoalCommand_EmitsNotices(t *testing.T) {
	srv, sess := goalTestServer(t)
	conn := subscribedConn(t, srv, sess.ID)
	srv.sender = &goalRoundSender{}

	srv.wsGoalCommand(sess.ID, "ship the release")

	ev := waitEventType(t, conn, "goal_notice")
	if ev["kind"] != "command" {
		t.Fatalf("first notice kind = %v, want command", ev["kind"])
	}
	if text, _ := ev["text"].(string); !strings.Contains(text, "Goal set") {
		t.Errorf("command notice text = %q, want the /goal reply", text)
	}

	// A brand-new goal "starts"; a resumed one would "continue".
	ev = waitEventType(t, conn, "goal_notice")
	if ev["kind"] != "start" {
		t.Errorf("second notice kind = %v, want start", ev["kind"])
	}
	if text, _ := ev["text"].(string); text != "" {
		t.Errorf("start notice carries text %q; the frontend owns that wording", text)
	}

	waitGoalTurnIdle(t, srv, sess.ID)
}

// Only genuine transitions are announced. Accounting re-broadcasts the goal on
// every tick, so a repeated status must stay silent, and the first status a
// session is seen in is a baseline — a page connecting to an already-blocked
// goal must not replay the transition that blocked it.
func TestGoalStatusTransitionNotices(t *testing.T) {
	srv, sess := goalTestServer(t)
	conn := subscribedConn(t, srv, sess.ID)

	blocked := agent.Goal{ID: "g1", Objective: "keep going", Status: agent.GoalBlocked}
	srv.broadcastGoalUpdated(sess.ID, blocked) // baseline — silent
	srv.broadcastGoalUpdated(sess.ID, blocked) // unchanged — silent

	done := blocked
	done.Status = agent.GoalComplete
	done.TokensUsed = 1200
	srv.broadcastGoalUpdated(sess.ID, done)

	// If either silent broadcast had spoken, this would be the blocked line.
	ev := waitEventType(t, conn, "goal_notice")
	if ev["kind"] != "status" {
		t.Fatalf("kind = %v, want status", ev["kind"])
	}
	if text, _ := ev["text"].(string); !strings.Contains(text, "Goal complete") {
		t.Errorf("text = %q, want the complete line", text)
	}
	if ev["level"] != "success" {
		t.Errorf("level = %v, want success", ev["level"])
	}

	// Clearing forgets the history: the next goal's first status is a fresh
	// baseline, not a change away from complete. Blocked is the probe because
	// it is announceable — a status the silent default branch would swallow
	// (active, paused) would pass whether or not the history was forgotten.
	srv.broadcastGoalCleared(sess.ID)
	limited := agent.Goal{ID: "g2", Objective: "next", Status: agent.GoalBlocked}
	srv.broadcastGoalUpdated(sess.ID, limited) // baseline — silent
	limited.Status = agent.GoalBudgetLimited
	limited.TokensUsed, limited.TokenBudget = 63900, 50000
	srv.broadcastGoalUpdated(sess.ID, limited)

	ev = waitEventType(t, conn, "goal_notice")
	if text, _ := ev["text"].(string); !strings.Contains(text, "63.9K/50K tokens") {
		t.Errorf("text = %q, want the budget line with usage; a blocked line here "+
			"means the cleared goal's status history outlived it", text)
	}
	if ev["level"] != "warning" {
		t.Errorf("level = %v, want warning", ev["level"])
	}
}

// The shared idle-turn helper's contract, tested on the helper rather than
// through one of its callers: a callback that declines must leave the session
// exactly as it found it — no turn, no binding held, and whatever queue the
// callback chose not to consume still intact for the next kick.
func TestKickIdleTurn_DeclineLeavesNothingHeld(t *testing.T) {
	srv, sess := goalTestServer(t)
	srv.enqueueSteer(sess.ID, agent.InboxItem{Text: "still waiting"})

	called := false
	if srv.kickIdleTurn(sess.ID, func(*agent.Session) (string, []agent.ContentBlock, bool) {
		called = true
		return "", nil, false
	}) {
		t.Fatal("kickIdleTurn reported a turn started for a declining callback")
	}
	if !called {
		t.Fatal("callback never ran")
	}

	mu := srv.sessionTurnLock(sess.ID)
	mu.Lock()
	running := srv.turnRunning[sess.ID]
	mu.Unlock()
	if running {
		t.Error("turnRunning stayed set after a declined kick")
	}
	if !srv.steerPending(sess.ID) {
		t.Error("the declined kick consumed the steer queue")
	}
	// The binding must be released. Probing from another entry, not from web
	// again: re-acquiring for the entry that already holds it succeeds either
	// way, so only a foreign entry can tell a released binding from a leaked
	// one. A leak locks the session away from the TUI until the lease expires.
	if ok, _, err := srv.acquireSessionBinding(sess.ID, agent.EntryTUI, false); !ok {
		t.Errorf("the declined kick kept the web binding: %v", err)
	} else {
		srv.releaseSessionBinding(sess.ID, agent.EntryTUI)
	}
}

// Queued user input outranks the goal: the kick stands down and leaves the
// message alone, so the turn it starts is the user's, and that turn's end is
// where the goal picks back up.
func TestKickIdleGoalTurn_DefersToQueuedSteer(t *testing.T) {
	srv, sess := goalTestServer(t)
	if _, err := sess.CreateGoal("keep going", 0); err != nil {
		t.Fatal(err)
	}
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	srv.enqueueSteer(sess.ID, agent.InboxItem{Text: "actually, do this first"})
	srv.sender = &goalRoundSender{}

	if srv.kickIdleGoalTurn(sess.ID, agent.GoalStartFresh) {
		t.Error("the goal kicked a turn over queued user input")
	}
	if !srv.steerPending(sess.ID) {
		t.Error("the queued message was consumed by the goal kick")
	}
}

func TestSteerPending(t *testing.T) {
	s := &Server{steerQueues: make(map[string][]queuedTurn)}
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
	srv.goalsEnabled.Store(true)

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
	srv.goalsEnabled.Store(false)
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
	srv.goalsEnabled.Store(true)
	ad := &fullFakeAdapter{}

	sess := srv.channelMgr.GetOrCreateSession(evFor("x"), nil)
	if _, err := sess.GoalStore().CreateGoal("keep going", 0); err != nil {
		t.Fatal(err)
	}

	srv.handleChannelMessage(context.Background(), ad, evFor("start"), nil)

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
	srv.goalsEnabled.Store(true)
	// Replace the factory-made zero-usage agent with one that reports usage.
	srv.channelMgr = channel.NewManager(&channel.Config{}, func(*agentprofile.Profile) *agent.Agent {
		return agent.New(&usageStubSender{}, "stub-model")
	}, channel.BindByChat)
	ad := &fullFakeAdapter{}

	sess := srv.channelMgr.GetOrCreateSession(evFor("x"), nil)
	if _, err := sess.GoalStore().CreateGoal("small budget", 50); err != nil {
		t.Fatal(err)
	}
	// Consume the mid-turn-creation skip so the first turn's usage bills.
	sess.GoalStore().ResetGoalWallClock()

	srv.handleChannelMessage(context.Background(), ad, evFor("start"), nil)

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
