package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/workflow"
)

func echoAgentOpts(_ context.Context, prompt string, _ workflow.AgentOptions) workflow.AgentResult {
	return workflow.AgentResult{Reply: "r<" + prompt + ">", OutputTokens: 1}
}

// waitForDone polls a manager run until it leaves "running", or fails on timeout.
func waitForDone(t *testing.T, m *WorkflowManager, id string) WorkflowRunSnapshot {
	t.Helper()
	// Generous wall-clock deadline: under `go test -race` the wasm interpreter
	// runs several times slower, so a tight iteration count flakes in CI.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		snap, ok := m.Read(id)
		if !ok {
			t.Fatalf("run %q vanished", id)
		}
		if snap.Status != "running" {
			return snap
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run did not finish within the polling window")
	return WorkflowRunSnapshot{}
}

func TestWorkflowManager_RunToCompletion(t *testing.T) {
	m := NewWorkflowManager()
	id, err := m.Start(WorkflowRunRequest{
		Description: "echo",
		Script:      `parallel(%w[a b]) { |x| agent(x) }.join(",")`,
		Agent:       echoAgentOpts,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	snap := waitForDone(t, m, id)
	if snap.Status != "done" {
		t.Fatalf("status = %q, want done (err=%q)", snap.Status, snap.ErrMsg)
	}
	if snap.Output != "r<a>,r<b>" {
		t.Errorf("Output = %q, want r<a>,r<b>", snap.Output)
	}
}

// #1140 follow-up: Start launches every run under a context detached from the
// caller (so it survives past the request that started it) — that detach used
// to silently drop WithWorkingDir along with it, so a script's own
// agent()/skill() calls (and anything nested they do, like workflow_save)
// fell back to the server's launch directory regardless of what directory the
// top-level workflow(name: ...) call was resolved from. WorkflowRunRequest.
// WorkingDir is what re-stamps it onto the detached context.
func TestWorkflowManager_Start_PropagatesWorkingDirIntoAgentCalls(t *testing.T) {
	m := NewWorkflowManager()
	var seenCWD string
	captureAgent := func(ctx context.Context, prompt string, _ workflow.AgentOptions) workflow.AgentResult {
		seenCWD = WorkingDir(ctx)
		return workflow.AgentResult{Reply: "ok"}
	}

	id, err := m.Start(WorkflowRunRequest{
		Description: "cwd-check",
		Script:      `agent("hi")`,
		Agent:       captureAgent,
		WorkingDir:  "/some/project/dir",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	snap := waitForDone(t, m, id)
	if snap.Status != "done" {
		t.Fatalf("status = %q, want done (err=%q)", snap.Status, snap.ErrMsg)
	}
	if seenCWD != "/some/project/dir" {
		t.Errorf("agent() call saw WorkingDir(ctx) = %q, want %q", seenCWD, "/some/project/dir")
	}
}

// An empty WorkingDir (a caller that never resolved one — shouldn't happen in
// practice since callers use WorkingDirOrCWD, but defends against a future
// caller that forgets to set it) must not panic and must leave the detached
// context with no working directory stamped, same as before this field
// existed.
func TestWorkflowManager_Start_EmptyWorkingDirIsANoOp(t *testing.T) {
	m := NewWorkflowManager()
	var sawEmpty bool
	captureAgent := func(ctx context.Context, prompt string, _ workflow.AgentOptions) workflow.AgentResult {
		sawEmpty = WorkingDir(ctx) == ""
		return workflow.AgentResult{Reply: "ok"}
	}

	id, err := m.Start(WorkflowRunRequest{
		Description: "no-cwd",
		Script:      `agent("hi")`,
		Agent:       captureAgent,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	snap := waitForDone(t, m, id)
	if snap.Status != "done" {
		t.Fatalf("status = %q, want done (err=%q)", snap.Status, snap.ErrMsg)
	}
	if !sawEmpty {
		t.Error("expected WorkingDir(ctx) to be empty when WorkingDir was never set")
	}
}

// TestWorkflowManager_Events verifies the live event sink (the web panel's feed)
// sees a started event, progress lines, and a terminal done event.
func TestWorkflowManager_Events(t *testing.T) {
	m := NewWorkflowManager()
	var mu sync.Mutex
	var kinds []string
	m.SetOnEvent(func(ev WorkflowEvent) {
		mu.Lock()
		kinds = append(kinds, ev.Kind)
		mu.Unlock()
	})
	id, err := m.Start(WorkflowRunRequest{
		Script: `log("step"); agent("x")`,
		Agent:  echoAgentOpts,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDone(t, m, id)
	// Give the terminal "done" emit a beat to land after status flips.
	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	var started, progress, done int
	for _, k := range kinds {
		switch k {
		case "started":
			started++
		case "progress":
			progress++
		case "done":
			done++
		}
	}
	if started != 1 || done != 1 || progress == 0 {
		t.Errorf("events = %v; want 1 started, >=1 progress, 1 done", kinds)
	}
}

// TestWorkflowManager_ConcurrencyCap verifies the manager refuses to exceed
// maxConcurrentWorkflows in-flight runs.
func TestWorkflowManager_ConcurrencyCap(t *testing.T) {
	m := NewWorkflowManager()
	block := make(chan struct{})
	blockingAgent := func(_ context.Context, _ string, _ workflow.AgentOptions) workflow.AgentResult {
		<-block
		return workflow.AgentResult{Reply: "done"}
	}
	started := 0
	for i := 0; i < maxConcurrentWorkflows; i++ {
		if _, err := m.Start(WorkflowRunRequest{Script: `agent("x")`, Agent: blockingAgent}); err != nil {
			t.Fatalf("Start %d: %v", i, err)
		}
		started++
	}
	if _, err := m.Start(WorkflowRunRequest{Script: `agent("x")`, Agent: blockingAgent}); err == nil {
		t.Error("expected the (cap+1)th Start to be refused")
	}
	close(block) // unblock so the goroutines exit
	_ = started
}

// ctxBlockingAgent blocks until the context is cancelled, then returns the
// cancellation error — so a Kill of the workflow actually unwinds it.
func ctxBlockingAgent(ctx context.Context, _ string, _ workflow.AgentOptions) workflow.AgentResult {
	<-ctx.Done()
	return workflow.AgentResult{Err: ctx.Err()}
}

// TestWorkflowManager_Kill verifies a running workflow can be cancelled by id
// and reports as killed.
func TestWorkflowManager_Kill(t *testing.T) {
	m := NewWorkflowManager()
	id, err := m.Start(WorkflowRunRequest{Script: `agent("x")`, Agent: ctxBlockingAgent})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	found, wasRunning := m.Kill(id)
	if !found || !wasRunning {
		t.Fatalf("Kill = (found=%v, wasRunning=%v), want (true, true)", found, wasRunning)
	}
	snap := waitForDone(t, m, id)
	if snap.Status != "error" || !strings.Contains(snap.ErrMsg, "killed") {
		t.Errorf("after kill: status=%q errMsg=%q, want error + 'killed'", snap.Status, snap.ErrMsg)
	}
}

// TestWorkflowManager_KillUnknownAndFinished covers the non-running cases.
func TestWorkflowManager_KillUnknownAndFinished(t *testing.T) {
	m := NewWorkflowManager()
	if found, _ := m.Kill("wf_999"); found {
		t.Error("Kill of unknown id should report found=false")
	}
	id, err := m.Start(WorkflowRunRequest{Script: `agent("x")`, Agent: echoAgentOpts})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDone(t, m, id)
	found, wasRunning := m.Kill(id)
	if !found || wasRunning {
		t.Errorf("Kill of finished run = (found=%v, wasRunning=%v), want (true, false)", found, wasRunning)
	}
}

// TestWorkflowManager_LastActivity verifies progress advances the liveness
// timestamp (so a stalled run is distinguishable from a busy one).
func TestWorkflowManager_LastActivity(t *testing.T) {
	m := NewWorkflowManager()
	id, err := m.Start(WorkflowRunRequest{Script: `log("step"); agent("x")`, Agent: echoAgentOpts})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	snap := waitForDone(t, m, id)
	if snap.LastActivity.IsZero() || snap.LastActivity.Before(snap.Start) {
		t.Errorf("LastActivity = %v, want a time at/after start %v", snap.LastActivity, snap.Start)
	}
}
