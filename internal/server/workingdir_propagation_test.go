package server

import (
	"context"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// TestPrepareToolTurn_CronSessionSelfHealsFromStaleInteractiveMode guards
// pre-existing cron task sessions, not just newly created ones: before
// write_file/edit_file stopped blanket-allowing $CWD, CreateSession
// persisted whatever the global default resolved to (often "interactive")
// onto every task session it created, and it only ever sets PermissionMode
// when creating a session, never when reusing one — so an old task's
// session file keeps saying "interactive" forever unless something at
// resolution time corrects it. Without the self-heal, this tick's
// write_file calls would hit a real ask with nobody to answer it (cron has
// no WS client), timing out to deny and silently breaking the task.
func TestPrepareToolTurn_CronSessionSelfHealsFromStaleInteractiveMode(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	dir := t.TempDir()
	a := agent.New(&stubSender{}, "qwen3.7-max")
	a.CWD = dir

	sess := agent.NewSession(a.Model, "")
	sess.Source = "cron"
	sess.PermissionMode = "interactive" // stale value from before this fix

	ctx, _, _, cleanup, err := srv.prepareToolTurn(context.Background(), a, sess)
	if err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	defer cleanup()

	allowed, reason := a.Gate.Check(ctx, "write_file", map[string]any{"path": dir + "/out.txt"})
	if !allowed {
		t.Errorf("write_file under a stale-interactive cron session: got denied (%q), want allowed (self-heal to auto)", reason)
	}
}

// An explicit strict/auto choice on a cron session must still be honored —
// the self-heal only ever corrects "interactive", which was never
// functional for an unattended tick in the first place.
func TestPrepareToolTurn_CronSessionHonorsExplicitStrictMode(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	dir := t.TempDir()
	a := agent.New(&stubSender{}, "qwen3.7-max")
	a.CWD = dir

	sess := agent.NewSession(a.Model, "")
	sess.Source = "cron"
	sess.PermissionMode = "strict"

	ctx, _, _, cleanup, err := srv.prepareToolTurn(context.Background(), a, sess)
	if err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	defer cleanup()

	allowed, _ := a.Gate.Check(ctx, "write_file", map[string]any{"path": dir + "/out.txt"})
	if allowed {
		t.Errorf("write_file under an explicit strict cron session: got allowed, want denied")
	}
}

// TestPrepareToolTurn_WiresWorkingDirFromAgentCWD guards the general (non-cron)
// server session path: before this, only cron tasks and worktree-isolated
// sub-agents ever stamped tools.WithWorkingDir into ctx, so a session whose
// working directory was retargeted away from the server's own launch
// directory (via PATCH /api/sessions/{id}/working_dir, or a non-default
// workspace_dir) hit the same "resolves from the wrong directory" bug #1140
// fixed for cron tasks — for every WorkingDir(ctx) consumer (read_file,
// write_file, edit_file, glob, grep, terminal, and the workflow registry),
// not just workflow lookup.
func TestPrepareToolTurn_WiresWorkingDirFromAgentCWD(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	a := agent.New(&stubSender{}, "qwen3.7-max")
	a.CWD = "/some/custom/project/dir"

	ctx, _, _, cleanup, err := srv.prepareToolTurn(context.Background(), a, nil)
	if err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	if got := tools.WorkingDir(ctx); got != a.CWD {
		t.Errorf("WorkingDir(ctx) = %q, want %q (a.CWD)", got, a.CWD)
	}
	if got := tools.ActiveWorkflowDiscoveryCWD(); got != a.CWD {
		t.Errorf("ActiveWorkflowDiscoveryCWD() = %q, want %q", got, a.CWD)
	}

	cleanup()
	if got := tools.ActiveWorkflowDiscoveryCWD(); got != "" {
		t.Errorf("ActiveWorkflowDiscoveryCWD() after cleanup = %q, want restored to \"\"", got)
	}
}

// A permission-engine failure returns before the spawner/manager globals
// (and now ActiveWorkflowDiscoveryCWD) are ever set — this pins that the
// early-return path really is early enough that there's nothing to leak, by
// confirming a normal, later call still starts from a clean slate.
func TestPrepareToolTurn_WorkflowDiscoveryCWDStartsCleanAcrossCalls(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	a1 := agent.New(&stubSender{}, "qwen3.7-max")
	a1.CWD = "/repo/A"
	_, _, _, cleanup1, err := srv.prepareToolTurn(context.Background(), a1, nil)
	if err != nil {
		t.Fatalf("prepareToolTurn (a1): %v", err)
	}
	cleanup1()

	if got := tools.ActiveWorkflowDiscoveryCWD(); got != "" {
		t.Fatalf("ActiveWorkflowDiscoveryCWD() after a1's cleanup = %q, want \"\"", got)
	}

	a2 := agent.New(&stubSender{}, "qwen3.7-max")
	a2.CWD = "/repo/B"
	_, _, _, cleanup2, err := srv.prepareToolTurn(context.Background(), a2, nil)
	if err != nil {
		t.Fatalf("prepareToolTurn (a2): %v", err)
	}
	defer cleanup2()

	if got := tools.ActiveWorkflowDiscoveryCWD(); got != "/repo/B" {
		t.Errorf("ActiveWorkflowDiscoveryCWD() during a2's turn = %q, want /repo/B", got)
	}
}
