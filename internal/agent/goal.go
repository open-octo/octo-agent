package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/open-octo/octo-agent/internal/prompt"
)

// GoalStatus is the lifecycle state of a session goal. Ownership is the core
// invariant: the user sets active/paused and clears; the model may only mark
// complete or blocked (via the update_goal tool); the system sets
// budget_limited (token budget crossed) and usage_limited (provider
// quota/rate-limit hit during goal-driven work).
type GoalStatus string

const (
	GoalActive        GoalStatus = "active"
	GoalPaused        GoalStatus = "paused"
	GoalBlocked       GoalStatus = "blocked"
	GoalUsageLimited  GoalStatus = "usage_limited"
	GoalBudgetLimited GoalStatus = "budget_limited"
	GoalComplete      GoalStatus = "complete"
)

// MaxGoalObjectiveChars caps the objective length (in runes, matching the
// Codex limit the feature is ported from).
const MaxGoalObjectiveChars = 4000

// Goal is a session's persistent objective: the agent keeps pursuing it
// across turns until the status machine stops the continuation loop. At most
// one goal exists per session; replacing it mints a new ID with fresh usage
// counters.
type Goal struct {
	// ID identifies this goal instance. A replaced goal gets a new ID, which
	// is what lets an in-flight continuation detect that the goal it queued
	// for no longer exists.
	ID        string     `json:"id"`
	Objective string     `json:"objective"`
	Status    GoalStatus `json:"status"`
	// TokenBudget is the optional spend ceiling; 0 means unbudgeted.
	TokenBudget int64 `json:"token_budget,omitempty"`
	// TokensUsed accumulates non-cached input + output tokens while the goal
	// was active (cache reads are deliberately free).
	TokensUsed int64 `json:"tokens_used"`
	// TimeUsedSeconds accumulates wall-clock seconds while the goal was active.
	TimeUsedSeconds int64     `json:"time_used_seconds"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// RemainingTokens reports the unspent budget, or -1 when unbudgeted.
func (g *Goal) RemainingTokens() int64 {
	if g.TokenBudget <= 0 {
		return -1
	}
	if rem := g.TokenBudget - g.TokensUsed; rem > 0 {
		return rem
	}
	return 0
}

// ValidateGoalObjective enforces the objective contract shared by every
// mutation surface (slash commands, tools, HTTP API).
func ValidateGoalObjective(objective string) error {
	if objective == "" {
		return fmt.Errorf("goal objective must not be empty")
	}
	if utf8.RuneCountInString(objective) > MaxGoalObjectiveChars {
		return fmt.Errorf("goal objective must be at most %d characters", MaxGoalObjectiveChars)
	}
	return nil
}

func validateGoalBudget(tokenBudget int64) error {
	if tokenBudget < 0 {
		return fmt.Errorf("goal token budget must be positive when provided")
	}
	return nil
}

// GoalAccountant receives goal usage accounting from the agent loop after
// each LLM reply. Implemented by Session; the session-owning layer wires it
// into Agent.GoalAcct so per-turn Agents (serve rebuilds one per turn) all
// account into the same durable record.
type GoalAccountant interface {
	// AccountGoalUsage folds a token delta (non-cached input + output) and
	// the elapsed wall-clock time into the goal, returning the updated goal
	// and whether the record changed.
	AccountGoalUsage(tokenDelta int64) (Goal, bool)
	// ResetGoalWallClock re-baselines the wall clock at turn start, dropping
	// the idle gap since the previous turn — idle time is not goal work.
	ResetGoalWallClock()
	// ConsumeGoalBudgetSteer returns the one-time budget-limit steering
	// prompt when the last accounting crossed the token budget, for the agent
	// loop to inject as a hidden steer.
	ConsumeGoalBudgetSteer() (string, bool)
	// ConsumeGoalObjectiveSteer returns the one-time steer staged when the
	// objective was edited while a goal existed, for the agent loop to inject
	// as a hidden steer so an in-flight turn adjusts to the new objective
	// instead of finishing out the stale one.
	ConsumeGoalObjectiveSteer() (string, bool)
}

// ResetGoalWallClock implements GoalAccountant: it restarts the wall-clock
// baseline for an accruing goal so time that passed between turns is dropped
// rather than billed. Called from the turn-start baseline reset; a stopped
// goal (no baseline) is left alone. A stale mid-turn-creation skip flag is
// dropped here too — at a turn boundary the token baseline is fresh, so the
// first accounting must bill normally.
func (s *Session) ResetGoalWallClock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.goalWallClockAt.IsZero() {
		s.goalWallClockAt = time.Now()
	}
	s.goalSkipNextTokenDelta = false
}

// GoalSnapshot returns a copy of the session's goal, or ok=false when none
// is set.
func (s *Session) GoalSnapshot() (Goal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Goal == nil {
		return Goal{}, false
	}
	return *s.Goal, true
}

// CreateGoal starts a new active goal. It fails when any goal exists —
// including a finished one; replacing is an explicit separate operation so
// the model-facing create_goal tool can never silently discard a goal.
func (s *Session) CreateGoal(objective string, tokenBudget int64) (Goal, error) {
	objective = strings.TrimSpace(objective)
	if err := ValidateGoalObjective(objective); err != nil {
		return Goal{}, err
	}
	if err := validateGoalBudget(tokenBudget); err != nil {
		return Goal{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Goal != nil {
		return Goal{}, fmt.Errorf("session already has a goal; update or clear it first")
	}
	return s.startGoalLocked(objective, tokenBudget), nil
}

// ReplaceGoal discards any existing goal and starts a fresh active one with
// a new ID and zeroed usage counters. Backs the user-confirmed
// "/goal <objective>" replace path.
func (s *Session) ReplaceGoal(objective string, tokenBudget int64) (Goal, error) {
	objective = strings.TrimSpace(objective)
	if err := ValidateGoalObjective(objective); err != nil {
		return Goal{}, err
	}
	if err := validateGoalBudget(tokenBudget); err != nil {
		return Goal{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Bank the outgoing goal's wall-clock tail before discarding it — the
	// tail is deliberately thrown away with the goal; accounting first keeps
	// the invariant that a goal's counters are final at every observation.
	s.accountGoalWallClockLocked()
	return s.startGoalLocked(objective, tokenBudget), nil
}

func (s *Session) startGoalLocked(objective string, tokenBudget int64) Goal {
	now := time.Now()
	s.Goal = &Goal{
		ID:          "goal-" + randomSuffix(now),
		Objective:   objective,
		Status:      GoalActive,
		TokenBudget: tokenBudget,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.goalWallClockAt = now
	s.resetGoalRuntimeLocked()
	// If a turn is running, its token baseline predates this goal; skip that
	// turn's next accounting tick so the creating round's context input is
	// not billed to the fresh goal. Cleared unconsumed at the next turn start.
	s.goalSkipNextTokenDelta = true
	records := []sessionRecord{{Type: "goal", Goal: s.Goal}}
	if s.Title == "" {
		// Seed the title from the objective (the Codex thread-preview
		// behavior). It needs its own record — a "goal" record doesn't carry
		// the title, so without one the seed would vanish on reload.
		s.Title = goalTitle(objective)
		records = append(records, sessionRecord{Type: "title", Title: s.Title})
	}
	s.appendRecordsLocked(records...)
	return *s.Goal
}

// EditGoalObjective rewrites the objective in place, preserving usage
// counters and budget. A budget_limited or complete goal re-activates on
// edit (the user is redefining what done means); other statuses are
// preserved so editing a paused goal does not silently resume it.
func (s *Session) EditGoalObjective(objective string) (Goal, error) {
	objective = strings.TrimSpace(objective)
	if err := ValidateGoalObjective(objective); err != nil {
		return Goal{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Goal == nil {
		return Goal{}, fmt.Errorf("no goal is currently set")
	}
	s.accountGoalWallClockLocked()
	s.Goal.Objective = objective
	if s.Goal.Status == GoalBudgetLimited || s.Goal.Status == GoalComplete {
		s.setGoalStatusLocked(GoalActive)
	}
	s.Goal.UpdatedAt = time.Now()
	s.resetGoalRuntimeLocked()
	// Stage the one-time steer so an in-flight turn adjusts to the new
	// objective on its next round instead of finishing out the stale one. If
	// no turn is running, the next turn's first accounting tick drains it —
	// same degradation the budget-limit steer already accepts.
	s.goalObjectiveSteer = WrapGoalContext(prompt.GoalObjectiveUpdated(goalPromptData(s.Goal)))
	s.appendGoalRecordLocked()
	return *s.Goal, nil
}

// SetGoalStatus applies a status change. Which transitions a caller may
// request is that caller's contract (slash commands pause/resume, the
// update_goal tool completes/blocks, the runtime limits); this method owns
// the invariants that hold regardless of caller: in-flight wall-clock time
// is accounted first, re-activating starts a fresh wall-clock baseline, an
// already-over-budget goal cannot re-enter active, and a completed goal
// cannot be reactivated this way — EditGoalObjective/ReplaceGoal are the
// only paths back from complete, matching what every UI surface offers a
// finished goal (edit/clear, never resume).
func (s *Session) SetGoalStatus(status GoalStatus) (Goal, error) {
	switch status {
	case GoalActive, GoalPaused, GoalBlocked, GoalUsageLimited, GoalBudgetLimited, GoalComplete:
	default:
		return Goal{}, fmt.Errorf("unknown goal status %q", status)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Goal == nil {
		return Goal{}, fmt.Errorf("no goal is currently set")
	}
	if status == GoalActive && s.Goal.Status == GoalComplete {
		return Goal{}, fmt.Errorf("goal is complete; edit the objective or replace it to resume work")
	}
	s.accountGoalWallClockLocked()
	s.setGoalStatusLocked(status)
	s.Goal.UpdatedAt = time.Now()
	s.resetGoalRuntimeLocked()
	s.appendGoalRecordLocked()
	return *s.Goal, nil
}

// setGoalStatusLocked applies the status plus the budget invariant: a goal
// at or over its budget cannot hold active — it lands on budget_limited.
func (s *Session) setGoalStatusLocked(status GoalStatus) {
	if status == GoalActive && goalOverBudget(s.Goal) {
		status = GoalBudgetLimited
	}
	s.Goal.Status = status
	if status == GoalActive || status == GoalBudgetLimited {
		if s.goalWallClockAt.IsZero() {
			s.goalWallClockAt = time.Now()
		}
	} else {
		s.goalWallClockAt = time.Time{}
	}
}

// ClearGoal deletes the goal. Reports whether one existed.
func (s *Session) ClearGoal() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Goal == nil {
		return false
	}
	s.accountGoalWallClockLocked()
	s.Goal = nil
	s.goalWallClockAt = time.Time{}
	s.resetGoalRuntimeLocked()
	s.appendGoalRecordLocked()
	return true
}

// resetGoalRuntimeLocked clears the continuation and steering runtime after
// any goal mutation: the zero-progress suppression ends (the user or an
// external actor changed something — a fresh audit is warranted) and a stale
// unconsumed budget or objective steer must not fire against the mutated
// goal. Callers that go on to stage a fresh steer (EditGoalObjective) do so
// after this reset.
func (s *Session) resetGoalRuntimeLocked() {
	s.goalContPending = false
	s.goalContSuppressed = false
	s.goalBudgetSteer = ""
	s.goalObjectiveSteer = ""
}

// AccountGoalUsage implements GoalAccountant: it folds a token delta and the
// wall-clock time elapsed since the last accounting into the goal. Usage
// accrues while the goal is active or budget_limited (in-flight work on a
// just-limited goal still costs tokens), but only an active goal *crosses*
// into budget_limited here. Persistence is best-effort — the mutation is
// in-memory first and the meta header carries the goal on the next rewrite,
// so a failed append loses durability, not state.
func (s *Session) AccountGoalUsage(tokenDelta int64) (Goal, bool) {
	if tokenDelta < 0 {
		tokenDelta = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.goalSkipNextTokenDelta {
		// The goal was created mid-turn: the agent's token baseline predates
		// it, so this tick's delta belongs to pre-goal work. Undershooting by
		// one round beats billing a whole context input to a fresh goal.
		s.goalSkipNextTokenDelta = false
		tokenDelta = 0
	}
	if s.Goal == nil || (s.Goal.Status != GoalActive && s.Goal.Status != GoalBudgetLimited) {
		return Goal{}, false
	}
	if tokenDelta > 0 {
		// Real token progress re-arms the continuation loop: the
		// zero-progress suppression exists to stop idle spinning, not to
		// block goals that are being worked on again.
		s.goalContSuppressed = false
	}
	timeDelta := s.goalWallClockDeltaLocked()
	if tokenDelta == 0 && timeDelta == 0 {
		return *s.Goal, false
	}
	s.Goal.TokensUsed += tokenDelta
	s.Goal.TimeUsedSeconds += timeDelta
	if s.Goal.Status == GoalActive && goalOverBudget(s.Goal) {
		s.Goal.Status = GoalBudgetLimited
		// Stage the one-time wrap-up steer for the agent loop to inject. Only
		// the accounting crossing steers — external mutations that land on
		// budget_limited (edit, resume-over-budget) happen outside a turn.
		s.goalBudgetSteer = WrapGoalContext(prompt.GoalBudgetLimit(goalPromptData(s.Goal)))
	}
	s.Goal.UpdatedAt = time.Now()
	s.appendGoalRecordLocked()
	return *s.Goal, true
}

// ConsumeGoalBudgetSteer implements GoalAccountant; see the interface doc.
func (s *Session) ConsumeGoalBudgetSteer() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	steer := s.goalBudgetSteer
	s.goalBudgetSteer = ""
	return steer, steer != ""
}

// ConsumeGoalObjectiveSteer implements GoalAccountant; see the interface doc.
func (s *Session) ConsumeGoalObjectiveSteer() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	steer := s.goalObjectiveSteer
	s.goalObjectiveSteer = ""
	return steer, steer != ""
}

// GoalContinuation reports whether an idle follow-up turn should start for
// the session's goal and returns the hidden prompt to start it with. Call it
// after a turn fully completes, when no other input is pending; enqueue the
// prompt as the next turn's user input.
//
// It owns the continuation policy: only an active goal continues; a
// continuation turn that accounted zero tokens suppresses further
// continuations until real token progress or a goal mutation re-arms them
// (the zero-progress guard — an idle-spinning loop must stop itself); and
// each hand-out is audited by the next call, so the caller needs no
// bookkeeping of its own.
func (s *Session) GoalContinuation() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Goal == nil || s.Goal.Status != GoalActive {
		s.goalContPending = false
		return "", false
	}
	if s.goalContPending {
		s.goalContPending = false
		if s.Goal.TokensUsed == s.goalContTokensAt {
			s.goalContSuppressed = true
		}
	}
	if s.goalContSuppressed {
		return "", false
	}
	s.goalContPending = true
	s.goalContTokensAt = s.Goal.TokensUsed
	return WrapGoalContext(prompt.GoalContinuation(goalPromptData(s.Goal))), true
}

// GoalContinuationPending reports whether the most recent turn was started by
// GoalContinuation and has not been audited yet. Transports use it to tell a
// failing continuation turn (mark the goal usage_limited on rate-limit
// errors, so the loop parks itself) from a failing user turn.
func (s *Session) GoalContinuationPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.goalContPending
}

// SuppressGoalContinuation parks the continuation loop without touching the
// goal itself. Transports call it when a turn was interrupted (the user said
// stop — continuing immediately would make the loop interrupt-proof) or
// errored (retrying an erroring turn unprompted is unbounded paid retries).
// The zero-progress audit can't catch either case: an aborted or errored
// turn usually still accounted partial tokens. The standard re-arms apply —
// real token progress from a later user turn, or any goal mutation.
func (s *Session) SuppressGoalContinuation() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.goalContPending = false
	s.goalContSuppressed = true
}

// IsRateLimitErr classifies a turn error as a provider rate/quota limit.
// Provider adapters surface non-2xx responses as "<vendor>: HTTP <code>: ..."
// (the retry layer has already retried transient 429s by the time one
// reaches here), so a sustained limit is matched on the status code plus the
// common textual variants gateways use. Transports use it to park a failing
// goal-continuation turn as usage_limited.
func IsRateLimitErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "http 429") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "rate_limit") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "quota")
}

// Markers wrapping every runtime-owned goal steering prompt injected as a
// user message. Model-facing; every UI surface strips them (see
// StripSystemReminders).
const (
	goalContextOpen  = "<goal_context>"
	goalContextClose = "</goal_context>"
)

// WrapGoalContext wraps a rendered steering prompt in the <goal_context>
// markers that hide it from UI surfaces while flagging its provenance to the
// model.
func WrapGoalContext(text string) string {
	return goalContextOpen + "\n" + text + "\n" + goalContextClose
}

func goalPromptData(g *Goal) prompt.GoalPromptData {
	return prompt.GoalPromptData{
		Objective:       g.Objective,
		TokensUsed:      g.TokensUsed,
		TokenBudget:     g.TokenBudget,
		TimeUsedSeconds: g.TimeUsedSeconds,
	}
}

// goalWallClockDeltaLocked returns whole elapsed seconds since the baseline
// and advances it by exactly the amount returned, so sub-second remainders
// carry over instead of being dropped on every accounting tick.
func (s *Session) goalWallClockDeltaLocked() int64 {
	if s.goalWallClockAt.IsZero() {
		return 0
	}
	secs := int64(time.Since(s.goalWallClockAt) / time.Second)
	if secs > 0 {
		s.goalWallClockAt = s.goalWallClockAt.Add(time.Duration(secs) * time.Second)
	}
	return secs
}

// accountGoalWallClockLocked folds pending wall-clock time into the goal
// before a mutation, so pausing or clearing does not lose the tail since the
// last accounting.
func (s *Session) accountGoalWallClockLocked() {
	if s.Goal == nil || (s.Goal.Status != GoalActive && s.Goal.Status != GoalBudgetLimited) {
		return
	}
	if secs := s.goalWallClockDeltaLocked(); secs > 0 {
		s.Goal.TimeUsedSeconds += secs
		s.Goal.UpdatedAt = time.Now()
	}
}

func goalOverBudget(g *Goal) bool {
	return g.TokenBudget > 0 && g.TokensUsed >= g.TokenBudget
}

// goalTitle derives a session title from the objective, mirroring how Codex
// seeds the thread preview from the goal.
func goalTitle(objective string) string {
	const maxLen = 60
	line := objective
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if r := []rune(line); len(r) > maxLen {
		return strings.TrimSpace(string(r[:maxLen-1])) + "…"
	}
	return line
}

// appendGoalRecordLocked persists the current goal (nil = cleared) as an
// append-only "goal" record, like lease records.
func (s *Session) appendGoalRecordLocked() {
	s.appendRecordsLocked(sessionRecord{Type: "goal", Goal: s.Goal})
}

// appendRecordsLocked appends records to the transcript. When the file is
// not on disk yet or is pending a rewrite, it does nothing: the meta header
// written by the next Save carries the goal (and title), and appending to an
// untrusted tail would corrupt the file. The on-disk check is by existence,
// not persisted count — a meta-only transcript (saved before its first
// message) has persisted == 0 but must still receive records, or a caller
// that never Saves again loses the mutation (the SetModelConfig trap).
// Errors are swallowed for the same reason the skip is safe: the in-memory
// state is authoritative and the next rewrite folds it in.
func (s *Session) appendRecordsLocked(records ...sessionRecord) {
	if s.forceRewrite {
		return
	}
	path, err := s.SavePath()
	if err != nil {
		return
	}
	if s.persisted == 0 {
		if _, err := os.Stat(path); err != nil {
			return // nothing on disk yet; the first Save writes the meta header
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, rec := range records {
		_ = enc.Encode(rec)
	}
}
