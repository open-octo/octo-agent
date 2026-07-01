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
	onEvent func(WorkflowEvent)
	onDone  func(WorkflowNotification)
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

	ctx, cancel := context.WithCancel(context.Background())

	m.mu.Lock()
	if m.active >= maxConcurrentWorkflows {
		n := m.active
		m.mu.Unlock()
		cancel()
		return "", fmt.Errorf("too many background workflows running (%d/%d) — wait for one to finish (poll workflow_status) before starting more", n, maxConcurrentWorkflows)
	}
	m.active++
	m.seq++
	id := fmt.Sprintf("wf_%d", m.seq)
	now := time.Now()
	run := &workflowRun{id: id, description: req.Description, cancel: cancel, start: now, lastActivity: now}
	m.runs[id] = run
	m.mu.Unlock()

	m.emit(WorkflowEvent{RunID: id, Description: req.Description, Kind: "started"})

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

// Read returns a snapshot of one run.
func (m *WorkflowManager) Read(id string) (WorkflowRunSnapshot, bool) {
	m.mu.Lock()
	run := m.runs[id]
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
	run := m.runs[id]
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
		b.WriteString(" Poll again later to collect the result.")
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
