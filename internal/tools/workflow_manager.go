package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/open-octo/octo-agent/internal/workflow"
)

// maxConcurrentWorkflows caps how many background workflows run at once per
// manager, so a model can't fan out an unbounded number of multi-agent runs.
const maxConcurrentWorkflows = 4

// maxWorkflowLogLines bounds the per-run log buffer retained for status reads.
const maxWorkflowLogLines = 500

// workflowPollStopThreshold is how many consecutive no-progress
// workflow_status reads escalate to a hard "stop polling" reminder. A read
// that observes fresh activity resets the count — spaced-out, user-prompted
// progress checks are legitimate; a third consecutive read that saw nothing
// new is a polling loop.
const workflowPollStopThreshold = 3

// WorkflowEvent is emitted as a background run progresses, for live display
// (the web panel). Kind is "started" | "progress" | "done".
type WorkflowEvent struct {
	RunID       string
	Description string
	Kind        string
	Line        string // the progress/log line for Kind=="progress"
	Status      string // "running" | "done" | "error" for Kind=="done"
}

// WorkflowNotification is delivered to the onDone hook when a background run
// finishes, so the transport can nudge the model on its next turn.
type WorkflowNotification struct {
	RunID        string
	Description  string
	Status       string // "done" | "error"
	Result       string // final output, or the error message
	JournalRunID string // the resume_from handle, when journaling was available
}

// WorkflowRunRequest starts one background run. The Agent func (spawner-backed)
// and limits are supplied by the caller so the manager stays decoupled from the
// tools.Spawner concrete type.
type WorkflowRunRequest struct {
	Description   string
	Script        string
	Args          string // the run's input value as a JSON string ("" = none)
	Agent         workflow.AgentFunc
	Skill         workflow.SkillFunc
	MaxConcurrent int
	ResumeFrom    string
	// Foreground marks a run the caller collects inline with Wait (the one-shot
	// blocking mode). The completion hook is skipped for such runs — the result
	// returns through the tool call, so a notification would be a duplicate.
	Foreground bool
	// WorkingDir is the directory this run's own tool calls (agent()'s
	// sub-agents, and any nested workflow()/workflow_save() they make) should
	// resolve relative paths and project-level workflows against. Start()
	// launches every run under a fresh, detached context (so it survives past
	// the request that started it) — WorkingDir is what lets that detached
	// context still carry the caller's WithWorkingDir value instead of
	// silently reverting to the server's own launch directory once the script
	// starts running (#1140 follow-up: the outer workflow(name: ...) lookup
	// was fixed to honor the caller's cwd, but a script's own internal calls
	// were still losing it here).
	WorkingDir string
	// JournalDir overrides the workflow runtime's journal directory
	// (~/.octo/workflow-journals by default). Empty leaves the runtime
	// default in place — real entry points never set this; tests point it at
	// a temp dir so running the suite doesn't write into a developer's real
	// journal directory (see ActiveWorkflowJournalDir).
	JournalDir string
}

// WorkflowRunSnapshot is a point-in-time view of a background run for listing
// and status reads.
type WorkflowRunSnapshot struct {
	ID           string
	Description  string
	Status       string // "running" | "done" | "error"
	Output       string
	ErrMsg       string
	Logs         []string
	JournalRunID string
	Start        time.Time
	End          time.Time
	// LastActivity is when the run last emitted progress (a log line or an
	// agent start/finish). A running run whose LastActivity is far in the past
	// is likely stuck — the gap, not the total elapsed, is the liveness signal.
	LastActivity time.Time
}

// workflowRun tracks one detached workflow.Run invocation.
type workflowRun struct {
	id          string
	description string
	cancel      context.CancelFunc
	start       time.Time
	foreground  bool
	finished    chan struct{} // closed by finish; Wait blocks on it

	mu           sync.Mutex
	done         bool
	errMsg       string
	output       string
	logs         []string
	journalRunID string
	end          time.Time
	lastActivity time.Time
	killed       bool
}

// appendLog records a progress line and marks the run live (updates
// lastActivity). now is passed in so the caller controls the clock.
func (r *workflowRun) appendLog(line string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, line)
	if n := len(r.logs) - maxWorkflowLogLines; n > 0 {
		r.logs = r.logs[n:]
	}
	r.lastActivity = now
}

// markKilled flags the run as deliberately killed, so finish reports it as a
// kill rather than a raw "context canceled" error.
func (r *workflowRun) markKilled() {
	r.mu.Lock()
	r.killed = true
	r.mu.Unlock()
}

func (r *workflowRun) finish(output, journalRunID, errMsg string, end time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.done = true
	r.output = output
	r.journalRunID = journalRunID
	// A killed run comes back as "context canceled"; report it as a kill so the
	// model doesn't read it as a spurious failure.
	if r.killed {
		errMsg = "workflow was killed (workflow_kill)"
	}
	r.errMsg = errMsg
	r.end = end
	close(r.finished)
}

// journalID returns the run's resume_from handle (empty until finish records it).
func (r *workflowRun) journalID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.journalRunID
}

func (r *workflowRun) snapshot() WorkflowRunSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := "running"
	if r.done {
		status = "done"
		if r.errMsg != "" {
			status = "error"
		}
	}
	logs := make([]string, len(r.logs))
	copy(logs, r.logs)
	return WorkflowRunSnapshot{
		ID:           r.id,
		Description:  r.description,
		Status:       status,
		Output:       r.output,
		ErrMsg:       r.errMsg,
		Logs:         logs,
		JournalRunID: r.journalRunID,
		Start:        r.start,
		End:          r.end,
		LastActivity: r.lastActivity,
	}
}

// WorkflowManager owns background workflow runs for one scope (process-global
// for CLI/TUI, or per-session for web/IM). It mirrors SubAgentManager: runs
// outlive the turn that launched them under a detached context.
type WorkflowManager struct {
	mu      sync.Mutex
	seq     int
	active  int
	runs    map[string]*workflowRun
	polls   map[string]pollState // no-progress workflow_status read streaks, by run id
	onEvent func(WorkflowEvent)
	onDone  func(WorkflowNotification)
}

// pollState tracks one run's streak of workflow_status reads that observed no
// new activity, for the anti-polling guard.
type pollState struct {
	count        int
	lastActivity time.Time
}

// NewWorkflowManager returns an empty manager.
func NewWorkflowManager() *WorkflowManager {
	return &WorkflowManager{runs: map[string]*workflowRun{}}
}

// SetOnEvent registers the live-progress sink (web panel). nil disables it.
func (m *WorkflowManager) SetOnEvent(fn func(WorkflowEvent)) {
	m.mu.Lock()
	m.onEvent = fn
	m.mu.Unlock()
}

// SetOnDone registers the completion hook (next-turn notification). nil disables it.
func (m *WorkflowManager) SetOnDone(fn func(WorkflowNotification)) {
	m.mu.Lock()
	m.onDone = fn
	m.mu.Unlock()
}

func (m *WorkflowManager) emit(ev WorkflowEvent) {
	m.mu.Lock()
	fn := m.onEvent
	m.mu.Unlock()
	if fn != nil {
		fn(ev)
	}
}

// Start launches req in the background and returns its handle id (e.g. "wf_1").
// The run executes under a detached context so it survives the turn.
func (m *WorkflowManager) Start(req WorkflowRunRequest) (string, error) {
	if req.Agent == nil {
		return "", fmt.Errorf("workflow: no agent dispatch configured")
	}

	// Detached from the caller's ctx (so the run survives past the turn that
	// started it), but re-stamped with the caller's working directory — a
	// bare context.Background() would otherwise strip it, and every tool call
	// the script's own agent()/skill() calls make (including a nested
	// workflow()/workflow_save()) would silently fall back to the server's
	// launch directory instead of req.WorkingDir.
	ctx, cancel := context.WithCancel(WithWorkingDir(context.Background(), req.WorkingDir))

	m.mu.Lock()
	if m.active >= maxConcurrentWorkflows {
		n := m.active
		m.mu.Unlock()
		cancel()
		return "", fmt.Errorf("too many workflows running (%d/%d) — wait for one to finish before starting more", n, maxConcurrentWorkflows)
	}
	m.active++
	m.seq++
	id := fmt.Sprintf("wf_%d", m.seq)
	now := time.Now()
	run := &workflowRun{
		id: id, description: req.Description, cancel: cancel, start: now,
		lastActivity: now, foreground: req.Foreground, finished: make(chan struct{}),
	}
	m.runs[id] = run
	m.mu.Unlock()

	m.emit(WorkflowEvent{RunID: id, Description: req.Description, Kind: "started"})

	// req.JournalDir is an explicit per-request override; ActiveWorkflowJournalDir
	// is the process-wide one (empty outside tests). Resolving the fallback here,
	// not in every caller that builds a WorkflowRunRequest, means a caller that
	// forgets to set it (or builds the request directly, bypassing WorkflowTool)
	// still respects the override — the tools package's tests rely on this to
	// avoid writing into a developer's real ~/.octo/workflow-journals.
	journalDir := req.JournalDir
	if journalDir == "" {
		journalDir = ActiveWorkflowJournalDir()
	}

	go func() {
		defer func() {
			m.mu.Lock()
			m.active--
			m.mu.Unlock()
		}()
		res, err := workflow.Run(ctx, req.Script, workflow.Options{
			Agent: req.Agent,
			Skill: req.Skill,
			Log: func(s string) {
				run.appendLog(s, time.Now())
				m.emit(WorkflowEvent{RunID: id, Kind: "progress", Line: s})
			},
			// Agent lifecycle ("→ start" / "✓ done") is also captured + counts as
			// activity, so a workflow with no log() calls still shows it's alive.
			Progress: func(s string) {
				run.appendLog(s, time.Now())
				m.emit(WorkflowEvent{RunID: id, Kind: "progress", Line: s})
			},
			MaxConcurrent: req.MaxConcurrent,
			ResumeFrom:    req.ResumeFrom,
			Args:          req.Args,
			JournalDir:    journalDir,
		})

		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		run.finish(res.Output, res.RunID, errMsg, time.Now())

		status := "done"
		if errMsg != "" {
			status = "error"
		}
		m.emit(WorkflowEvent{RunID: id, Description: req.Description, Kind: "done", Status: status})

		m.mu.Lock()
		hook := m.onDone
		m.mu.Unlock()
		// A foreground run's result returns through the blocked tool call
		// (Wait); a completion notification would reach the model twice.
		if run.foreground {
			hook = nil
		}
		if hook != nil {
			result := res.Output
			if errMsg != "" {
				result = errMsg
			}
			hook(WorkflowNotification{
				RunID:        id,
				Description:  req.Description,
				Status:       status,
				Result:       result,
				JournalRunID: res.RunID,
			})
		}
	}()

	return id, nil
}

// RecordStatusRead tracks workflow_status reads of a still-running run so the
// status tool can escalate to a hard "stop polling" reminder — the workflow
// counterpart of terminal_output's empty-snapshot guard. Only reads that
// observe NO new activity since the previous read extend the streak; a read
// that sees progress (lastActivity advanced) starts a fresh one, so spaced-out
// user-prompted checks of a live run never escalate. Returns the streak count;
// a read of a finished run resets its state.
func (m *WorkflowManager) RecordStatusRead(id string, running bool, lastActivity time.Time) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !running {
		delete(m.polls, id)
		return 0
	}
	if m.polls == nil {
		m.polls = map[string]pollState{}
	}
	st := m.polls[id]
	if lastActivity.After(st.lastActivity) {
		st.count = 0
		st.lastActivity = lastActivity
	}
	st.count++
	m.polls[id] = st
	return st.count
}

// Wait blocks until the run finishes or ctx is cancelled. On cancellation it
// cancels the run (unwinding its in-flight sub-agents), waits for the unwind
// to complete so no goroutine outlives the call unaccounted, and returns ctx's
// error alongside the final snapshot. Backs the foreground (one-shot) mode of
// the workflow tool.
func (m *WorkflowManager) Wait(ctx context.Context, id string) (WorkflowRunSnapshot, error) {
	m.mu.Lock()
	run := m.runs[id]
	m.mu.Unlock()
	if run == nil {
		return WorkflowRunSnapshot{}, fmt.Errorf("no workflow run %q", id)
	}
	select {
	case <-run.finished:
		return run.snapshot(), nil
	case <-ctx.Done():
		run.cancel()
		<-run.finished
		return run.snapshot(), ctx.Err()
	}
}

// resolveLocked finds a run by its short alias (the map key, e.g. "wf_1") or by
// its JournalRunID (the "wf-YYYYMMDD-HHMMSS-xxxxxxxx" handle that failure notices
// print and tell the model to pass to resume_from). Both identify the same run,
// so a lookup tool must accept either — the model naturally retries with whatever
// id the last message showed it. Caller holds m.mu.
func (m *WorkflowManager) resolveLocked(id string) *workflowRun {
	// A running run's journalID is "" until it finishes, so an empty id would
	// otherwise match the first still-running run in the scan below — a lookup
	// (or a Kill) of "" must find nothing. Callers already reject empty ids, but
	// keep the invariant inside the resolver so a future caller can't trip it.
	if id == "" {
		return nil
	}
	if run := m.runs[id]; run != nil {
		return run
	}
	for _, run := range m.runs {
		if run.journalID() == id {
			return run
		}
	}
	return nil
}

// Read returns a snapshot of one run.
func (m *WorkflowManager) Read(id string) (WorkflowRunSnapshot, bool) {
	m.mu.Lock()
	run := m.resolveLocked(id)
	m.mu.Unlock()
	if run == nil {
		return WorkflowRunSnapshot{}, false
	}
	return run.snapshot(), true
}

// List returns snapshots of every run this manager knows, oldest first.
func (m *WorkflowManager) List() []WorkflowRunSnapshot {
	m.mu.Lock()
	runs := make([]*workflowRun, 0, len(m.runs))
	for _, r := range m.runs {
		runs = append(runs, r)
	}
	m.mu.Unlock()

	out := make([]WorkflowRunSnapshot, 0, len(runs))
	for _, r := range runs {
		out = append(out, r.snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// Kill cancels one running workflow by id. Returns (found, wasRunning): found
// is false for an unknown id; wasRunning is false when the run had already
// finished (a no-op cancel). The run's detached context is cancelled, which
// propagates to its in-flight sub-agents and unwinds the script.
func (m *WorkflowManager) Kill(id string) (found, wasRunning bool) {
	m.mu.Lock()
	run := m.resolveLocked(id)
	m.mu.Unlock()
	if run == nil {
		return false, false
	}
	run.mu.Lock()
	done := run.done
	run.mu.Unlock()
	if done {
		return true, false
	}
	run.markKilled()
	run.cancel()
	return true, true
}

// KillAll cancels every running workflow this manager owns. Called on session
// close so a detached run doesn't outlive its conversation.
func (m *WorkflowManager) KillAll() {
	m.mu.Lock()
	runs := make([]*workflowRun, 0, len(m.runs))
	for _, r := range m.runs {
		runs = append(runs, r)
	}
	m.mu.Unlock()
	for _, r := range runs {
		r.cancel()
	}
}

// statusLine renders a one-line summary of a run for listings. For a running
// run it appends how long since the last activity, so a stalled run (large
// idle gap) is distinguishable from one making steady progress.
func statusLine(s WorkflowRunSnapshot) string {
	elapsed := time.Since(s.Start)
	if s.Status != "running" {
		elapsed = s.End.Sub(s.Start)
	}
	desc := s.Description
	if desc == "" {
		desc = "(workflow)"
	}
	line := fmt.Sprintf("%s  [%s]  %s  (%s)", s.ID, s.Status, desc, elapsed.Round(time.Second))
	if s.Status == "running" && !s.LastActivity.IsZero() {
		line += fmt.Sprintf("  · last activity %s ago", time.Since(s.LastActivity).Round(time.Second))
	}
	return line
}

// formatRunDetail renders a single run's full status for workflow_status.
func formatRunDetail(s WorkflowRunSnapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", statusLine(s))
	switch s.Status {
	case "running":
		idle := time.Since(s.LastActivity)
		if s.LastActivity.IsZero() {
			idle = 0
		}
		fmt.Fprintf(&b, "Still running — last activity %s ago, %d progress line(s) so far.",
			idle.Round(time.Second), len(s.Logs))
		if idle > 2*time.Minute {
			b.WriteString(" If the idle gap keeps growing it may be stuck — workflow_kill(run_id) cancels it.")
		}
		b.WriteString(" You will be notified automatically when it finishes — do not poll.")
		if n := len(s.Logs); n > 0 {
			fmt.Fprintf(&b, "\n\n[progress]\n%s", strings.Join(s.Logs, "\n"))
		}
	case "error":
		b.WriteString("\n")
		// A script error is the model's own Ruby — say so plainly so it fixes
		// the script and re-runs rather than treating the run as terminally broken.
		if strings.Contains(s.ErrMsg, "script error") {
			b.WriteString("The Ruby script failed to run. Fix the script and call workflow again.\n\n")
			b.WriteString(s.ErrMsg)
			// Offer resume only when agents actually ran (progress logged); a
			// compile/syntax error journals nothing, so resume would be a no-op.
			if s.JournalRunID != "" && len(s.Logs) > 0 {
				fmt.Fprintf(&b, "\n\nSome agents completed before the failure — pass resume_from: %q "+
					"to skip re-running them.", s.JournalRunID)
			}
		} else {
			b.WriteString(s.ErrMsg)
		}
	default: // done
		b.WriteString("\n")
		b.WriteString(s.Output)
		if s.JournalRunID != "" {
			fmt.Fprintf(&b, "\n\n[workflow run: %s]", s.JournalRunID)
		}
		if n := len(s.Logs); n > 0 {
			fmt.Fprintf(&b, "\n\n[workflow log]\n%s", strings.Join(s.Logs, "\n"))
		}
	}
	return b.String()
}
