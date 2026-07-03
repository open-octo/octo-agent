package tools

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestDefaultTools_WorkflowGatedOnSpawner verifies the workflow + workflow_status
// tools are only advertised when a Spawner is registered (nothing to delegate to
// otherwise).
func TestDefaultTools_WorkflowGatedOnSpawner(t *testing.T) {
	SetSpawner(nil)
	t.Cleanup(func() { SetSpawner(nil) })

	if advertisedNames()["workflow"] || advertisedNames()["workflow_status"] || advertisedNames()["workflow_kill"] {
		t.Error("workflow tools should be absent when no Spawner is configured")
	}
	SetSpawner(&fakeSpawner{})
	for _, name := range []string{"workflow", "workflow_status", "workflow_kill"} {
		if !advertisedNames()[name] {
			t.Errorf("%s should appear once a Spawner is registered", name)
		}
	}
}

// replySpawner echoes the prompt so tests can assert the agent()→Spawner path.
type replySpawner struct{}

func (replySpawner) Spawn(_ context.Context, req SpawnRequest) (SpawnResult, error) {
	return SpawnResult{Reply: "R[" + req.Prompt + "]", OutputTokens: 3}, nil
}
func (replySpawner) Continue(_ context.Context, _, _ string) (SpawnResult, error) {
	return SpawnResult{}, nil
}

var wfRunIDRe = regexp.MustCompile(`wf_\d+`)

// startWorkflowAndWait starts a background workflow via the tool, then polls
// workflow_status by run id until it is no longer running, returning the final
// status text. Fails the test on timeout.
func startWorkflowAndWait(t *testing.T, input map[string]any) string {
	t.Helper()
	res, err := WorkflowTool{}.Execute(context.Background(), "c", input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	id := wfRunIDRe.FindString(res.Text)
	if id == "" {
		t.Fatalf("no run id in start result: %q", res.Text)
	}
	// Generous wall-clock deadline: under `go test -race` the wasm interpreter
	// runs several times slower, so a tight iteration count flakes in CI.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		out, err := WorkflowStatusTool{}.Execute(context.Background(), "c", map[string]any{"run_id": id})
		if err != nil {
			t.Fatalf("workflow_status: %v", err)
		}
		if !strings.Contains(out.Text, "[running]") {
			return out.Text
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("workflow did not finish within the polling window")
	return ""
}

// TestWorkflowTool_StartsInBackground verifies the tool returns a run handle
// immediately rather than blocking on the result.
func TestWorkflowTool_StartsInBackground(t *testing.T) {
	SetSpawner(replySpawner{})
	t.Cleanup(func() { SetSpawner(nil) })

	res, err := WorkflowTool{}.Execute(context.Background(), "c", map[string]any{
		"script": `parallel(%w[a b c]) { |x| agent(x) }.join(",")`,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Text, "background") || wfRunIDRe.FindString(res.Text) == "" {
		t.Errorf("start result should name a background run id; got %q", res.Text)
	}
	// The behavioral steer the anti-poll design relies on: promise the
	// completion notification and forbid polling.
	if !strings.Contains(res.Text, "DO NOT poll") || !strings.Contains(res.Text, "automatically notify") {
		t.Errorf("start result should forbid polling and promise the completion notification; got %q", res.Text)
	}
}

// TestWorkflowTool_StatusCollectsResult drives the full async path: start, then
// collect the completed result via workflow_status.
func TestWorkflowTool_StatusCollectsResult(t *testing.T) {
	SetSpawner(replySpawner{})
	t.Cleanup(func() { SetSpawner(nil) })

	got := startWorkflowAndWait(t, map[string]any{
		"script": `parallel(%w[a b c]) { |x| agent(x) }.join(",")`,
	})
	if !strings.Contains(got, "[done]") || !strings.Contains(got, "R[a],R[b],R[c]") {
		t.Errorf("status = %q, want a done run with R[a],R[b],R[c]", got)
	}
}

// TestWorkflowTool_StatusListsRuns verifies the no-argument listing form.
func TestWorkflowTool_StatusListsRuns(t *testing.T) {
	SetSpawner(replySpawner{})
	t.Cleanup(func() { SetSpawner(nil) })

	_ = startWorkflowAndWait(t, map[string]any{
		"script":      `agent("x")`,
		"description": "list me",
	})
	out, err := WorkflowStatusTool{}.Execute(context.Background(), "c", map[string]any{})
	if err != nil {
		t.Fatalf("workflow_status list: %v", err)
	}
	if !strings.Contains(out.Text, "list me") {
		t.Errorf("listing should include the run description; got %q", out.Text)
	}
}

// ctxBlockingSpawner blocks each Spawn until the context is cancelled, so a
// workflow built on it stays running until killed (and unwinds cleanly on kill).
type ctxBlockingSpawner struct{}

func (ctxBlockingSpawner) Spawn(ctx context.Context, _ SpawnRequest) (SpawnResult, error) {
	<-ctx.Done()
	return SpawnResult{}, ctx.Err()
}
func (ctxBlockingSpawner) Continue(_ context.Context, _, _ string) (SpawnResult, error) {
	return SpawnResult{}, nil
}

// TestWorkflowTool_Kill drives the full kill path through the tools: start a
// workflow whose agent blocks, cancel it with workflow_kill, and confirm
// workflow_status reports it killed.
func TestWorkflowTool_Kill(t *testing.T) {
	SetSpawner(ctxBlockingSpawner{})
	t.Cleanup(func() { SetSpawner(nil) })

	res, err := WorkflowTool{}.Execute(context.Background(), "c", map[string]any{"script": `agent("x")`})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	id := wfRunIDRe.FindString(res.Text)
	if id == "" {
		t.Fatalf("no run id in %q", res.Text)
	}

	kr, err := WorkflowKillTool{}.Execute(context.Background(), "c", map[string]any{"run_id": id})
	if err != nil {
		t.Fatalf("workflow_kill: %v", err)
	}
	if !strings.Contains(kr.Text, "Cancelled") {
		t.Errorf("kill result = %q, want a cancellation confirmation", kr.Text)
	}

	// Poll until it leaves running, then confirm it reads as killed.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := WorkflowStatusTool{}.Execute(context.Background(), "c", map[string]any{"run_id": id})
		if !strings.Contains(out.Text, "[running]") {
			if !strings.Contains(out.Text, "killed") {
				t.Errorf("final status = %q, want it to mention 'killed'", out.Text)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("killed workflow never left running")
}

const wfStopMarker = "[STOP: repeated workflow_status polling detected"

// TestWorkflowManager_RecordStatusRead pins the anti-poll streak semantics:
// no-progress reads accumulate, a read that observes fresh activity restarts
// the streak, a finished-run read clears it, and runs are counted independently.
func TestWorkflowManager_RecordStatusRead(t *testing.T) {
	m := NewWorkflowManager()
	t0 := time.Now()

	if got := m.RecordStatusRead("wf_1", true, t0); got != 1 {
		t.Fatalf("first read = %d, want 1", got)
	}
	if got := m.RecordStatusRead("wf_1", true, t0); got != 2 {
		t.Fatalf("no-progress read = %d, want 2", got)
	}
	// Another run's streak is independent.
	if got := m.RecordStatusRead("wf_2", true, t0); got != 1 {
		t.Fatalf("other run's first read = %d, want 1", got)
	}
	// Fresh activity restarts the streak — a spaced-out check of a live run
	// must never escalate.
	if got := m.RecordStatusRead("wf_1", true, t0.Add(time.Second)); got != 1 {
		t.Fatalf("read after progress = %d, want 1", got)
	}
	if got := m.RecordStatusRead("wf_1", true, t0.Add(time.Second)); got != 2 {
		t.Fatalf("no-progress read after reset = %d, want 2", got)
	}
	// A finished-run read clears the state.
	if got := m.RecordStatusRead("wf_1", false, t0.Add(time.Second)); got != 0 {
		t.Fatalf("finished read = %d, want 0", got)
	}
	if got := m.RecordStatusRead("wf_1", true, t0.Add(time.Second)); got != 1 {
		t.Fatalf("read after finish-reset = %d, want 1", got)
	}
}

// startBlockedRun starts a workflow whose single agent blocks until cancelled,
// on an isolated manager, and waits for the launch progress line so
// LastActivity is stable across subsequent status reads (a blocked agent emits
// nothing further). Returns the ctx carrying the manager and the run id.
func startBlockedRun(t *testing.T, mgr *WorkflowManager) (context.Context, string) {
	t.Helper()
	SetSpawner(ctxBlockingSpawner{})
	t.Cleanup(func() { SetSpawner(nil) })
	ctx := WithWorkflowManager(context.Background(), mgr)

	res, err := WorkflowTool{}.Execute(ctx, "c", map[string]any{"script": `agent("x")`})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	id := wfRunIDRe.FindString(res.Text)
	if id == "" {
		t.Fatalf("no run id in %q", res.Text)
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if snap, ok := mgr.Read(id); ok && len(snap.Logs) > 0 {
			return ctx, id
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("workflow never logged its launch")
	return ctx, id
}

// TestWorkflowStatus_AntiPollingGuard verifies that repeated no-progress status
// reads of a still-running run escalate to a hard STOP reminder, and that a
// read of the finished run carries no reminder (the state resets).
func TestWorkflowStatus_AntiPollingGuard(t *testing.T) {
	mgr := NewWorkflowManager()
	ctx, id := startBlockedRun(t, mgr)

	for i := 1; i < workflowPollStopThreshold; i++ {
		out, err := WorkflowStatusTool{}.Execute(ctx, "c", map[string]any{"run_id": id})
		if err != nil {
			t.Fatalf("workflow_status #%d: %v", i, err)
		}
		if strings.Contains(out.Text, wfStopMarker) {
			t.Fatalf("read #%d should not carry the STOP reminder yet: %q", i, out.Text)
		}
	}
	out, err := WorkflowStatusTool{}.Execute(ctx, "c", map[string]any{"run_id": id})
	if err != nil {
		t.Fatalf("workflow_status at threshold: %v", err)
	}
	if !strings.Contains(out.Text, wfStopMarker) {
		t.Errorf("read #%d should carry the STOP reminder; got %q", workflowPollStopThreshold, out.Text)
	}

	// Finish the run; a read of a completed run must not carry the reminder.
	if _, err := (WorkflowKillTool{}).Execute(ctx, "c", map[string]any{"run_id": id}); err != nil {
		t.Fatalf("workflow_kill: %v", err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := WorkflowStatusTool{}.Execute(ctx, "c", map[string]any{"run_id": id})
		if !strings.Contains(out.Text, "[running]") {
			if strings.Contains(out.Text, wfStopMarker) {
				t.Errorf("finished-run read should not carry the STOP reminder: %q", out.Text)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("killed workflow never left running")
}

// TestWorkflowStatus_AntiPollingGuard_ListForm verifies the no-argument list
// form escalates the same way — a model must not dodge the per-run guard by
// polling the list instead.
func TestWorkflowStatus_AntiPollingGuard_ListForm(t *testing.T) {
	mgr := NewWorkflowManager()
	ctx, id := startBlockedRun(t, mgr)
	defer mgr.Kill(id)

	for i := 1; i < workflowPollStopThreshold; i++ {
		out, err := WorkflowStatusTool{}.Execute(ctx, "c", map[string]any{})
		if err != nil {
			t.Fatalf("workflow_status list #%d: %v", i, err)
		}
		if strings.Contains(out.Text, wfStopMarker) {
			t.Fatalf("list read #%d should not carry the STOP reminder yet: %q", i, out.Text)
		}
	}
	out, err := WorkflowStatusTool{}.Execute(ctx, "c", map[string]any{})
	if err != nil {
		t.Fatalf("workflow_status list at threshold: %v", err)
	}
	if !strings.Contains(out.Text, wfStopMarker) {
		t.Errorf("list read #%d should carry the STOP reminder; got %q", workflowPollStopThreshold, out.Text)
	}
}

// TestWorkflowKill_UnknownRun verifies the tool errors on an unknown id.
func TestWorkflowKill_UnknownRun(t *testing.T) {
	SetSpawner(replySpawner{})
	t.Cleanup(func() { SetSpawner(nil) })
	_, err := WorkflowKillTool{}.Execute(context.Background(), "c", map[string]any{"run_id": "wf_nope_999"})
	if err == nil || !strings.Contains(err.Error(), "no run named") {
		t.Errorf("err = %v, want unknown-run error", err)
	}
}

// TestDefaultWorkflowOnDone_FiresOnCompletion guards the CLI/TUI notification
// path: a workflow started through the tool (which resolves to the default
// manager) must fire the OnDone hook on completion. This is the wiring that was
// missing — without it the agent never learns a background run finished.
func TestDefaultWorkflowOnDone_FiresOnCompletion(t *testing.T) {
	SetSpawner(replySpawner{})
	t.Cleanup(func() { SetSpawner(nil) })

	done := make(chan WorkflowNotification, 1)
	SetDefaultWorkflowOnDone(func(ev WorkflowNotification) { done <- ev })
	t.Cleanup(func() { SetDefaultWorkflowOnDone(nil) })

	_, err := WorkflowTool{}.Execute(context.Background(), "c",
		map[string]any{"script": `agent("x")`, "description": "notify-me"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	select {
	case ev := <-done:
		if ev.Status != "done" || ev.Description != "notify-me" {
			t.Errorf("notification = %+v, want status=done description=notify-me", ev)
		}
		note := FormatWorkflowNote(ev)
		for _, want := range []string{"<system-reminder>", "notify-me", "workflow_status"} {
			if !strings.Contains(note, want) {
				t.Errorf("FormatWorkflowNote missing %q; got:\n%s", want, note)
			}
		}
	case <-time.After(60 * time.Second):
		t.Fatal("OnDone hook did not fire within the window")
	}
}

func TestWorkflowTool_RefusesInSubAgent(t *testing.T) {
	SetSpawner(replySpawner{})
	t.Cleanup(func() { SetSpawner(nil) })

	ctx := WithSubAgentMarker(context.Background())
	_, err := WorkflowTool{}.Execute(ctx, "c", map[string]any{"script": `"x"`})
	if err == nil || !strings.Contains(err.Error(), "cannot run a workflow") {
		t.Errorf("err = %v, want sub-agent refusal", err)
	}
}

func TestWorkflowTool_RequiresScript(t *testing.T) {
	SetSpawner(replySpawner{})
	t.Cleanup(func() { SetSpawner(nil) })

	_, err := WorkflowTool{}.Execute(context.Background(), "c", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "provide a script, or a name") {
		t.Errorf("err = %v, want script-or-name required", err)
	}
}

func TestWorkflowTool_ScriptAndNameMutuallyExclusive(t *testing.T) {
	SetSpawner(replySpawner{})
	t.Cleanup(func() { SetSpawner(nil) })

	_, err := WorkflowTool{}.Execute(context.Background(), "c",
		map[string]any{"script": `"x"`, "name": "foo"})
	if err == nil || !strings.Contains(err.Error(), "exactly one of script or name") {
		t.Errorf("err = %v, want mutual-exclusion error", err)
	}
}

// TestWorkflowTool_ScriptErrorIsActionable verifies a bad Ruby script surfaces
// through workflow_status as a fix-and-retry instruction with the mruby
// position noise stripped, so the model self-corrects instead of giving up.
func TestWorkflowTool_ScriptErrorIsActionable(t *testing.T) {
	SetSpawner(replySpawner{})
	t.Cleanup(func() { SetSpawner(nil) })

	got := startWorkflowAndWait(t, map[string]any{"script": `this_is_not_defined(1)`})
	if !strings.Contains(got, "[error]") {
		t.Fatalf("status should report an error run; got %q", got)
	}
	for _, want := range []string{"Fix the script and call workflow again", "this_is_not_defined"} {
		if !strings.Contains(got, want) {
			t.Errorf("error status = %q, want substring %q", got, want)
		}
	}
	if strings.Contains(got, "(unknown)") {
		t.Errorf("error status leaks mruby position noise: %q", got)
	}
}

// TestWorkflowTool_RunsSavedByName runs a workflow from the registry by name and
// confirms args flow through to the script (the saved script reads args["q"]).
func TestWorkflowTool_RunsSavedByName(t *testing.T) {
	SetSpawner(replySpawner{})
	t.Cleanup(func() { SetSpawner(nil) })
	user := t.TempDir()
	useWorkflowRoots(t, user, "")
	writeWorkflowFile(t, user, "echo.rb", "# @description echo the arg\nagent(args[\"q\"])\n")

	got := startWorkflowAndWait(t, map[string]any{"name": "echo", "args": map[string]any{"q": "hello"}})
	if !strings.Contains(got, "R[hello]") {
		t.Errorf("output = %q, want the saved script to run with args (R[hello])", got)
	}
}

func TestWorkflowTool_UnknownName(t *testing.T) {
	SetSpawner(replySpawner{})
	t.Cleanup(func() { SetSpawner(nil) })
	useWorkflowRoots(t, t.TempDir(), t.TempDir())

	_, err := WorkflowTool{}.Execute(context.Background(), "c", map[string]any{"name": "nope"})
	if err == nil || !strings.Contains(err.Error(), "no saved workflow named") {
		t.Errorf("err = %v, want unknown-name error", err)
	}
}
