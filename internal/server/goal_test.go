package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

func TestIsRateLimitErr(t *testing.T) {
	for _, err := range []error{
		errors.New("anthropic: HTTP 429: rate limited"),
		errors.New("openai: HTTP 429 (insufficient_quota): You exceeded your current quota"),
		errors.New("agent: loop[3]: send: Rate limit reached for requests"),
		errors.New("gateway: too many requests"),
	} {
		if !isRateLimitErr(err) {
			t.Errorf("should classify as rate limit: %v", err)
		}
	}
	for _, err := range []error{
		nil,
		errors.New("anthropic: HTTP 500: overloaded"),
		errors.New("context deadline exceeded"),
	} {
		if isRateLimitErr(err) {
			t.Errorf("should NOT classify as rate limit: %v", err)
		}
	}
}

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
