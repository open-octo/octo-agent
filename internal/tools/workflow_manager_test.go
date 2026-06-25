package tools

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Leihb/octo-agent/internal/workflow"
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
