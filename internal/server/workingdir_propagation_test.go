package server

import (
	"context"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// A cron run's mode is decided once, by newSession — prepareToolTurn must not
// second-guess a cron session's own mode. The case this pins: a person
// continues a run's session in the Web UI and switches it to interactive;
// asks must then reach them rather than being silently auto-approved.
func TestPrepareToolTurn_CronSessionHonorsInteractive(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS() // the ask goes out over the WS hub
	srv.pendingConfirms = map[string]wsEventRequestConfirmation{}

	dir := t.TempDir()
	a := agent.New(&stubSender{}, "qwen3.7-max")
	a.CWD = dir

	sess := agent.NewSession(a.Model, "")
	sess.Source = "cron"
	sess.PermissionMode = "interactive"

	ctx, _, _, cleanup, err := srv.prepareToolTurn(prepareToolTurnCtx(t, "cron-interactive"), a, sess)
	if err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	defer cleanup()

	// Nobody answers the ask, so it ends with the context: denied, not
	// auto-approved.
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	allowed, _ := a.Gate.Check(ctx, "write_file", map[string]any{"path": dir + "/out.txt"})
	if allowed {
		t.Errorf("write_file under an interactive cron session: got allowed, want an unanswered ask")
	}
}

// An explicit strict on a cron session is honored the same way.
func TestPrepareToolTurn_CronSessionHonorsExplicitStrictMode(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	dir := t.TempDir()
	a := agent.New(&stubSender{}, "qwen3.7-max")
	a.CWD = dir

	sess := agent.NewSession(a.Model, "")
	sess.Source = "cron"
	sess.PermissionMode = "strict"

	ctx, _, _, cleanup, err := srv.prepareToolTurn(prepareToolTurnCtx(t, "cron-strict"), a, sess)
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

	ctx, _, _, cleanup, err := srv.prepareToolTurn(prepareToolTurnCtx(t, "workingdir"), a, nil)
	if err != nil {
		t.Fatalf("prepareToolTurn: %v", err)
	}
	if got := tools.WorkingDir(ctx); got != a.CWD {
		t.Errorf("WorkingDir(ctx) = %q, want %q (a.CWD)", got, a.CWD)
	}
	cleanup()
}
