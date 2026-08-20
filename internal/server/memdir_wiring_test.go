package server

import (
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/memory"
	"github.com/open-octo/octo-agent/internal/scheduler"
)

// These tests go through the registry rather than calling sessionMemDir with a
// hand-written argument, because the defect class here is a caller passing a
// working directory where a project directory belongs. A test that supplies the
// argument itself cannot catch that — it asserts the function, not the wiring.
//
// What they cover is the pairing of sessionProjectDir and sessionMemDir: that a
// session's project, not its own directory, is what selects its memory. They do
// NOT drive buildAgent or an IM turn, so they would not catch a regression
// confined to one of those call sites. In practice the compiler covers that
// gap — projectDir is bound once per turn and used for the prompt injection, the
// hook injector, and the write roots, so swapping any one of them back to cwd
// leaves it unused and the package does not build.

// A session filed under a project resolves to that project's memory, and the
// session's OWN working directory has no say in it.
func TestSessionMemDir_WiredThroughTheRegistry(t *testing.T) {
	setTestHome(t)
	homeMem, err := memory.HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	projectDir := t.TempDir()
	// The session's own directory is somewhere else entirely, so a resolution
	// that reads it instead of the project cannot accidentally pass.
	ownDir := t.TempDir()

	g, err := createSessionGroupNamed("proj", projectDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if g.WorkingDir == "" {
		t.Fatal("fixture: group did not become a project")
	}
	sess := agent.NewSession("m", "")
	sess.WorkingDir = ownDir
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	if err := addSessionToGroup(g.ID, sess.ID); err != nil {
		t.Fatal(err)
	}

	srv := &Server{cwd: t.TempDir(), homeMemDir: homeMem}
	got := srv.sessionMemDir(srv.sessionProjectDir(sess.ID))

	want, err := memory.Dir(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("memory dir = %q, want the project's %q", got, want)
	}
	if own, err := memory.Dir(ownDir); err == nil && got == own {
		t.Error("memory dir followed the session's own working dir, not its project")
	}
	if got == homeMem {
		t.Error("a project session fell back to the shared tier")
	}
}

// The same session, moved out of the project, reads the shared tier — a
// directory it merely runs in does not earn memory of its own.
func TestSessionMemDir_UngroupedSessionReadsSharedTier(t *testing.T) {
	setTestHome(t)
	homeMem, err := memory.HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	sess := agent.NewSession("m", "")
	sess.WorkingDir = t.TempDir()
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}

	srv := &Server{cwd: t.TempDir(), homeMemDir: homeMem}
	if got := srv.sessionMemDir(srv.sessionProjectDir(sess.ID)); got != homeMem {
		t.Errorf("memory dir = %q, want the shared tier %q", got, homeMem)
	}
}

// A cron task with a directory works on that directory every single run, so its
// notes belong to it. Before its group carried the directory, every run's notes
// went to the tier every session on the machine reads — unattended, repeatedly,
// which is the pollution scoping by project exists to prevent.
func TestCronTask_RunsGetProjectMemory(t *testing.T) {
	setTestHome(t)
	homeMem, err := memory.HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.homeMemDir = homeMem

	g, err := createSessionGroupNamed("nightly", dir, "cron-1")
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := srv.CreateSession(scheduler.Task{Name: "nightly", Directory: dir, SessionGroupID: g.ID})
	if err != nil {
		t.Fatal(err)
	}

	got := srv.sessionMemDir(srv.sessionProjectDir(sessionID))
	want, err := memory.Dir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("cron run memory dir = %q, want the task's own %q", got, want)
	}
	if got == homeMem {
		t.Error("a cron run's notes went to the shared tier")
	}
}

// A task with no directory has no project either: nothing to scope memory to,
// so the shared tier is correct.
func TestCronTask_WithoutDirectoryStaysAPlainGroup(t *testing.T) {
	setTestHome(t)
	homeMem, err := memory.HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	g, err := createSessionGroupNamed("dirless", "", "cron-2")
	if err != nil {
		t.Fatal(err)
	}
	if g.WorkingDir != "" {
		t.Fatalf("group should stay plain, got working dir %q", g.WorkingDir)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.homeMemDir = homeMem
	sessionID, err := srv.CreateSession(scheduler.Task{Name: "dirless", SessionGroupID: g.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := srv.sessionMemDir(srv.sessionProjectDir(sessionID)); got != homeMem {
		t.Errorf("memory dir = %q, want the shared tier %q", got, homeMem)
	}
}

// An unusable directory must not stop a task from running; it degrades to a
// plain group, which is the pre-existing behaviour for grouping failures.
func TestCronTask_UnusableDirectoryDegradesToPlainGroup(t *testing.T) {
	setTestHome(t)
	g, err := createSessionGroupNamed("bad-dir", filepath.Join(t.TempDir(), "does-not-exist"), "cron-3")
	if err != nil {
		t.Fatalf("an unusable directory must not fail group creation: %v", err)
	}
	if g.WorkingDir != "" {
		t.Errorf("working dir = %q, want it dropped", g.WorkingDir)
	}
}
