package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"
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
}

// ResetGoalWallClock implements GoalAccountant: it restarts the wall-clock
// baseline for an accruing goal so time that passed between turns is dropped
// rather than billed. Called from the turn-start baseline reset; a stopped
// goal (no baseline) is left alone.
func (s *Session) ResetGoalWallClock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.goalWallClockAt.IsZero() {
		s.goalWallClockAt = time.Now()
	}
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
	s.appendGoalRecordLocked()
	return *s.Goal, nil
}

// SetGoalStatus applies a status change. Which transitions a caller may
// request is that caller's contract (slash commands pause/resume, the
// update_goal tool completes/blocks, the runtime limits); this method owns
// the invariants that hold regardless of caller: in-flight wall-clock time
// is accounted first, re-activating starts a fresh wall-clock baseline, and
// an already-over-budget goal cannot re-enter active.
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
	s.accountGoalWallClockLocked()
	s.setGoalStatusLocked(status)
	s.Goal.UpdatedAt = time.Now()
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
	s.appendGoalRecordLocked()
	return true
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
	if s.Goal == nil || (s.Goal.Status != GoalActive && s.Goal.Status != GoalBudgetLimited) {
		return Goal{}, false
	}
	timeDelta := s.goalWallClockDeltaLocked()
	if tokenDelta == 0 && timeDelta == 0 {
		return *s.Goal, false
	}
	s.Goal.TokensUsed += tokenDelta
	s.Goal.TimeUsedSeconds += timeDelta
	if s.Goal.Status == GoalActive && goalOverBudget(s.Goal) {
		s.Goal.Status = GoalBudgetLimited
	}
	s.Goal.UpdatedAt = time.Now()
	s.appendGoalRecordLocked()
	return *s.Goal, true
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
