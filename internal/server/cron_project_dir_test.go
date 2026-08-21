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
func TestWorkspaceDirForTask_OnePerTaskUnderTheWorkspace(t *testing.T) {
	workspace := t.TempDir()

	a, err := workspaceDirForTask(nil, workspace, "daily report", "task-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := workspaceDirForTask(nil, workspace, "weekly digest", "task-2")
	if err != nil {
		t.Fatal(err)
	}

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

	// A candidate another group already claims steps aside; the task's own
	// directory is reused as-is.
	claimed := []sessionGroup{{ID: "g-proj", Name: "proj", WorkingDir: filepath.Join(workspace, "daily report")}}
	c, err := workspaceDirForTask(claimed, workspace, "daily report", "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if c != filepath.Join(workspace, "daily report-2") {
		t.Errorf("claimed-name dir = %q, want the suffixed candidate", c)
	}
	claimed = append(claimed, sessionGroup{ID: "g-cron", Name: "daily report", WorkingDir: c, TaskID: "task-1"})
	d, err := workspaceDirForTask(claimed, workspace, "daily report", "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if d != c {
		t.Errorf("the task's own directory was suffixed: %q, want %q", d, c)
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
func TestWorkspaceDirForTask_FallsBackToTheTaskID(t *testing.T) {
	workspace := t.TempDir()

	got, err := workspaceDirForTask(nil, workspace, "///", "task-9")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(workspace, "task-9") {
		t.Errorf("dir = %q, want the task id under the workspace", got)
	}
}

// An explicit directory on the task still means what its author meant — the
// deliverables land there — but as the output mount of a generated workspace,
// the same shape every project has (see createCronProject's test for the full
// contract).
func TestCronTaskDir_ExplicitDirectoryBecomesOutputMount(t *testing.T) {
	setTestHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.setWorkspaceDir(t.TempDir())
	explicit := t.TempDir()

	g, err := srv.createCronProject(scheduler.Task{Name: "daily report", ID: "task-1", Directory: explicit})
	if err != nil {
		t.Fatal(err)
	}
	if g.OutputDir != explicit || len(g.SourceDirs) != 1 || g.SourceDirs[0] != explicit {
		t.Errorf("explicit directory not the output mount: %+v", g)
	}
	if g.WorkingDir == explicit {
		t.Errorf("explicit directory was adopted as the workspace itself")
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
	if p := projectForSession(run.ID); p == nil || p.WorkingDir != want {
		t.Errorf("run resolves to %+v, want working dir %q", p, want)
	}
}
