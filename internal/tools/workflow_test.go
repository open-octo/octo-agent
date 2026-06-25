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

	if advertisedNames()["workflow"] || advertisedNames()["workflow_status"] {
		t.Error("workflow tools should be absent when no Spawner is configured")
	}
	SetSpawner(&fakeSpawner{})
	if !advertisedNames()["workflow"] || !advertisedNames()["workflow_status"] {
		t.Error("workflow + workflow_status should appear once a Spawner is registered")
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
	if err == nil || !strings.Contains(err.Error(), "script is required") {
		t.Errorf("err = %v, want script-required", err)
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
