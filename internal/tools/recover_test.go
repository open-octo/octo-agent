package tools

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/workflow"
)

// The goroutines in this package have no request scope above them to recover,
// and the desktop app runs the server in-process — so a panic in any of them
// used to take the whole app down. These tests pin both halves of the fix: the
// process survives, AND whatever the goroutine was responsible for settling
// gets settled, because a recover that skipped that would turn each crash into
// a hang.

// panicSpawner panics instead of running an agent.
type panicSpawner struct{}

func (panicSpawner) Spawn(context.Context, SpawnRequest) (SpawnResult, error) {
	panic("spawner exploded")
}

func (panicSpawner) Continue(context.Context, string, string) (SpawnResult, error) {
	panic("continue exploded")
}

func TestRunSync_PanickingSpawnerReturnsError(t *testing.T) {
	mgr := NewSubAgentManager(panicSpawner{})
	mgr.SetSynchronous(true)

	done := make(chan error, 1)
	go func() {
		_, err := mgr.RunSync(context.Background(), SpawnRequest{Description: "boom"})
		done <- err
	}()

	select {
	case err := <-done:
		// The tool call must come back with the failure, not with a nil error.
		if err == nil {
			t.Fatal("RunSync returned no error after the spawner panicked")
		}
		if !strings.Contains(err.Error(), "panicked") {
			t.Errorf("error should name the panic, got: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunSync never returned: the panic became a hang")
	}

	// The sync slot must have been released too, or the next sub-agent call
	// would block forever behind the dead one.
	second := make(chan struct{})
	go func() {
		_, _ = mgr.RunSync(context.Background(), SpawnRequest{Description: "after"})
		close(second)
	}()
	select {
	case <-second:
	case <-time.After(10 * time.Second):
		t.Fatal("a later RunSync blocked: the concurrency slot leaked")
	}
}

func TestStart_PanickingSpawnerClearsBusy(t *testing.T) {
	mgr := NewSubAgentManager(panicSpawner{})

	id, err := mgr.Start(SpawnRequest{Description: "boom"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// setDone is the only thing that ever clears the busy flag; the goal loop's
	// idle check reads it, so an agent stuck "working" wedges more than the UI.
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, status, found := mgr.Read(id)
		if !found {
			t.Fatalf("agent %s disappeared", id)
		}
		if status != "running" {
			if !strings.Contains(status, "panicked") {
				t.Errorf("status should report the panic, got %q", status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent still reports running: the panic left it busy forever")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if running := mgr.ListRunning(); len(running) != 0 {
		t.Errorf("ListRunning still reports %d agent(s) after the panic", len(running))
	}
}

func TestSend_PanickingContinueClearsBusy(t *testing.T) {
	// Spawn succeeds, then the follow-up message panics — the path a user hits
	// by replying to a sub-agent that is already parked.
	mgr := NewSubAgentManager(&spawnOKContinuePanicSpawner{})

	id, err := mgr.Start(SpawnRequest{Description: "ok"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitAgentIdle(t, mgr, id)

	if err := mgr.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitAgentIdle(t, mgr, id)
}

// spawnOKContinuePanicSpawner completes a spawn normally but panics on the
// follow-up round.
type spawnOKContinuePanicSpawner struct{}

func (*spawnOKContinuePanicSpawner) Spawn(context.Context, SpawnRequest) (SpawnResult, error) {
	return SpawnResult{AgentID: "c1", Reply: "hi"}, nil
}

func (*spawnOKContinuePanicSpawner) Continue(context.Context, string, string) (SpawnResult, error) {
	panic("continue exploded")
}

func waitAgentIdle(t *testing.T, mgr *SubAgentManager, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, status, found := mgr.Read(id); found && status != "running" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("agent stayed busy: the panic was recovered but never settled")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Named for what it actually covers: the panic happens in the goroutine
// internal/workflow spawns per agent() call, not in the manager's run
// goroutine, so this pins the workflow runtime's own settle path.
func TestWorkflow_PanickingAgentReachesScriptAndFinishes(t *testing.T) {
	m := NewWorkflowManager()

	id, err := m.Start(WorkflowRunRequest{
		Description: "boom",
		Script:      `agent("x")`,
		Agent: func(context.Context, string, workflow.AgentOptions) workflow.AgentResult {
			panic("agent exploded")
		},
		JournalDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Polling rather than Wait on purpose: finish is what closes the channel
	// Wait blocks on, and Wait's own cancellation path waits on that same
	// channel — so a regression here would hang the test binary instead of
	// failing it.
	deadline := time.Now().Add(60 * time.Second)
	for {
		snap, ok := m.Read(id)
		if !ok {
			t.Fatalf("run %s disappeared", id)
		}
		if snap.Status != "running" {
			// A failed agent() is an ordinary outcome for a script — it comes
			// back as an "[agent error] …" string (runtime.go) — so the panic
			// must arrive that way too, rather than as a lost token.
			if !strings.Contains(snap.Output, "panicked") && !strings.Contains(snap.ErrMsg, "panicked") {
				t.Errorf("panic never reached the script; output=%q errMsg=%q", snap.Output, snap.ErrMsg)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("run never finished: nothing would ever release a Wait on it")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The manager's own run goroutine, whose reachable panic source is the
// completion hook it calls after the run has finished.
func TestWorkflowManager_PanickingDoneHookSurvives(t *testing.T) {
	m := NewWorkflowManager()
	m.SetOnDone(func(WorkflowNotification) { panic("done hook exploded") })

	id, err := m.Start(WorkflowRunRequest{
		Description: "hookboom",
		Script:      `"ok"`,
		Agent: func(context.Context, string, workflow.AgentOptions) workflow.AgentResult {
			return workflow.AgentResult{Reply: "unused"}
		},
		JournalDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(60 * time.Second)
	for {
		snap, ok := m.Read(id)
		if !ok {
			t.Fatalf("run %s disappeared", id)
		}
		if snap.Status != "running" {
			// The hook panics after finish, so the result must be intact — the
			// notification is what failed, not the run.
			if snap.Status != "done" {
				t.Errorf("status = %q, want done", snap.Status)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("run never finished")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestBackgroundManager_PanickingOutputHandlerStillExits(t *testing.T) {
	m := NewBackgroundManager()

	// The output handler panics on the first line, with plenty of output still
	// to come. Draining the pipe is what this goroutine really owns: cmd.Stdout
	// is an *io.PipeWriter, so os/exec copies into it from its own goroutine and
	// Wait blocks on that copy — a reader that dies without closing its end
	// leaves the copy blocked forever, and with it Wait, finish, and the exit
	// notification. That is the crash-turned-hang this whole change exists to
	// avoid, so it gets a regression test of its own.
	id, err := m.Start(context.Background(), unfinishedOutputCommand(), BgModeAsync,
		WithOnLine(func(string) { panic("output handler exploded") }))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(hookTimeout())
	for {
		_, status, found, _, _ := m.Read(id)
		if !found {
			t.Fatalf("process %s disappeared", id)
		}
		if status != "running" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("process still running: the reader died without releasing the pipe, so cmd.Wait never returned")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// unfinishedOutputCommand returns a shell command that emits a line, pauses,
// then emits another. The pause is what makes the test deterministic: the
// reader panics on the first line while the second is still unwritten, so the
// copy goroutine is guaranteed to be mid-Write against a pipe nobody drains.
// Sheer volume works too, but only past whatever buffering happens to be in
// play — 2000 lines slipped through and passed against the broken code.
func unfinishedOutputCommand() string {
	if runtime.GOOS == "windows" {
		return "Write-Output a; Start-Sleep -Seconds 1; Write-Output b"
	}
	return "echo a; sleep 1; echo b"
}

func TestBackgroundManager_PanickingExitHookSurvives(t *testing.T) {
	m := NewBackgroundManager()
	m.SetOnExit(func(BgExit) { panic("hook exploded") })

	id, err := m.Start(context.Background(), "echo hi", BgModeAsync)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// The hook fires after the exit is recorded, so the process must still be
	// observable as finished — and the manager must still be usable.
	deadline := time.Now().Add(hookTimeout())
	for {
		_, status, found, _, _ := m.Read(id)
		if !found {
			t.Fatalf("process %s disappeared", id)
		}
		if status != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("process never reported an exit after the hook panicked")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if _, err := m.Start(context.Background(), "echo again", BgModeAsync); err != nil {
		t.Fatalf("manager unusable after a panicking hook: %v", err)
	}
}
