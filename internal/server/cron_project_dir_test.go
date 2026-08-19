package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/scheduler"
)

// A scheduled task works in its own directory under the workspace, created on
// demand. Before this its runs had no working directory at all and fell through
// to wherever `octo serve` was started from — every task on the machine writing
// into the same place.
func TestCronProjectDir_OnePerTaskUnderTheWorkspace(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	workspace := t.TempDir()
	srv.setWorkspaceDir(workspace)

	a := srv.cronProjectDir("daily report", "task-1")
	b := srv.cronProjectDir("weekly digest", "task-2")

	if a != filepath.Join(workspace, "daily report") {
		t.Errorf("dir = %q, want %q", a, filepath.Join(workspace, "daily report"))
	}
	if a == b {
		t.Error("two tasks share one directory")
	}
	for _, d := range []string{a, b} {
		info, err := os.Stat(d)
		if err != nil || !info.IsDir() {
			t.Errorf("%q was not created as a directory: %v", d, err)
		}
	}
}

// The directory has to survive being used as a path: a task name is free text.
func TestDirNameFor(t *testing.T) {
	cases := []struct{ in, want string }{
		{"daily report", "daily report"},
		{"每日播报", "每日播报"},                     // non-ASCII is a fine directory name
		{"deploy/staging", "deploy-staging"}, // separators cannot survive
		{"a\\b", "a-b"},
		{"9:00 report", "9-00 report"}, // reserved on Windows
		{"what?", "what"},
		{".ssh", "ssh"}, // no hidden directories
		{"  padded  ", "padded"},
		{"trailing.", "trailing"}, // Windows refuses a trailing dot
		{"///", ""},               // nothing usable left; the caller falls back to the task id
	}
	for _, c := range cases {
		if got := dirNameFor(c.in); got != c.want {
			t.Errorf("dirNameFor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A name that reduces to nothing still gets a directory, named by the task id —
// otherwise the runs would silently fall back to the server's launch directory,
// which is the bug this whole path exists to fix.
func TestCronProjectDir_FallsBackToTheTaskID(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	workspace := t.TempDir()
	srv.setWorkspaceDir(workspace)

	got := srv.cronProjectDir("///", "task-9")
	if got != filepath.Join(workspace, "task-9") {
		t.Errorf("dir = %q, want the task id under the workspace", got)
	}
}

// An explicit directory on the task wins: someone who set one meant it.
func TestCronTaskDir_ExplicitDirectoryWins(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.setWorkspaceDir(t.TempDir())
	explicit := t.TempDir()

	got := srv.cronTaskDir(scheduler.Task{Name: "daily report", ID: "task-1", Directory: explicit})
	if got != explicit {
		t.Errorf("dir = %q, want the task's own %q", got, explicit)
	}
}

// A cluster written before scheduled tasks were projects gets its directory at
// startup rather than being left running in the server's launch directory.
func TestDissolvePlainGroups_BackfillsTheCronDirectory(t *testing.T) {
	setTestHome(t)
	run := saveSessionIn(t, "")
	writeGroups(t, sessionGroup{ID: "g-cron", Name: "daily report", SessionIDs: []string{run.ID}})

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	workspace := t.TempDir()
	srv.setWorkspaceDir(workspace)
	srv.initScheduler()
	if srv.scheduler == nil {
		t.Skip("scheduler unavailable")
	}
	task := scheduler.Task{Name: "daily report", Cron: "0 0 9 * * *", Prompt: "report", Enabled: true}
	if err := srv.scheduler.Add(&task); err != nil {
		t.Fatalf("add task: %v", err)
	}
	if err := srv.scheduler.SetSessionGroup(task.ID, "g-cron"); err != nil {
		t.Fatalf("set session group: %v", err)
	}

	srv.dissolvePlainGroups()

	got := groupByName(t, "daily report")
	want := filepath.Join(workspace, "daily report")
	if got.WorkingDir != want {
		t.Errorf("working dir = %q, want %q", got.WorkingDir, want)
	}
	// And the run now resolves to it, which is the point.
	if dir := srv.sessionProjectDir(run.ID); dir != want {
		t.Errorf("run resolves to %q, want %q", dir, want)
	}
}
