package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/trash"
)

// A dirless non-project session gets its own throwaway workspace under
// <workspace>/tasks/<sessionID> — not the shared workspace root, where two
// tasks writing the same filename overwrote each other.
func TestTaskWorkspace_SeededPerSession(t *testing.T) {
	srv := groupTestServer(t)
	sess := agent.NewSession("m", "")
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}

	srv.applyDefaultWorkspaceDir(sess)

	want := filepath.Join(srv.curWorkspaceDir(), "tasks", sess.ID)
	if sess.WorkingDir != want {
		t.Fatalf("seeded dir = %q, want %q", sess.WorkingDir, want)
	}
	if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
		t.Errorf("task workspace was not created: %v", err)
	}
	// And resolution runs the task there — its own dir, nothing shadows it.
	if got := srv.sessionCwd(sess); got != want {
		t.Errorf("cwd = %q, want the task workspace %q", got, want)
	}
}

// Sessions already carrying a dir, or born inside a project, are untouched.
func TestTaskWorkspace_SeedSkipsOwnedAndProjectSessions(t *testing.T) {
	srv := groupTestServer(t)

	own := agent.NewSession("m", "")
	own.WorkingDir = t.TempDir()
	srv.applyDefaultWorkspaceDir(own)
	if filepath.Dir(own.WorkingDir) == filepath.Join(srv.curWorkspaceDir(), "tasks") {
		t.Error("a session with its own dir was reseeded")
	}

	inProject := agent.NewSession("m", "")
	if err := inProject.Save(); err != nil {
		t.Fatal(err)
	}
	gid := newProjectGroup(t, srv, "Work", t.TempDir())
	fileInProject(t, gid, inProject.ID)
	srv.applyDefaultWorkspaceDir(inProject)
	if inProject.WorkingDir != "" {
		t.Errorf("a project session was seeded with %q, want none", inProject.WorkingDir)
	}
}

// A task workspace is a SEEDED value: a task later filed into a project must
// run in the project's workspace, not keep running in its throwaway directory
// while the re-frozen prompt claims otherwise.
func TestTaskWorkspace_ProjectShadowsSeededTaskDir(t *testing.T) {
	srv := groupTestServer(t)
	sess := agent.NewSession("m", "")
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	srv.applyDefaultWorkspaceDir(sess)

	gid, ws := newProjectGroupWS(t, srv, "Work", t.TempDir())
	fileInProject(t, gid, sess.ID)

	if got := srv.sessionCwd(sess); got != ws {
		t.Errorf("task filed into a project: cwd = %q, want the project workspace %q", got, ws)
	}
}

// Task workspaces are never adopted into projects: the adoption pass skips
// everything under the workspace root, not just the root itself.
func TestAdoptTaskWorkingDirs_SkipsTaskWorkspaces(t *testing.T) {
	srv := groupTestServer(t)
	taskDir := filepath.Join(srv.curWorkspaceDir(), "tasks", "20260101-000000-deadbeef")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sess := agent.NewSession("m", "")
	sess.WorkingDir = taskDir
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}

	srv.adoptTaskWorkingDirs()

	if p := projectForSession(sess.ID); p != nil {
		t.Errorf("a task workspace was adopted into project %+v", p)
	}
}

// Deleting a session takes its throwaway workspace to the trash — and ONLY a
// directory under <workspace>/tasks/ is ever handled this way.
func TestTaskWorkspace_TrashedOnSessionDelete(t *testing.T) {
	srv := groupTestServer(t)
	sess := agent.NewSession("m", "")
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	srv.applyDefaultWorkspaceDir(sess)
	dir := sess.WorkingDir
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}

	if _, failed := srv.deleteSessionsByID([]string{sess.ID}); len(failed) != 0 {
		t.Fatalf("delete failed: %v", failed)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("task workspace still on disk after delete: %v", err)
	}
	entries, err := trash.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Original == dir {
			found = true
		}
	}
	if !found {
		t.Errorf("deleted task workspace not in trash: %+v", entries)
	}

	// A session whose dir is NOT under the tasks root keeps its directory.
	keep := agent.NewSession("m", "")
	keepDir := t.TempDir()
	keep.WorkingDir = keepDir
	if err := keep.Save(); err != nil {
		t.Fatal(err)
	}
	if _, failed := srv.deleteSessionsByID([]string{keep.ID}); len(failed) != 0 {
		t.Fatalf("delete failed: %v", failed)
	}
	if _, err := os.Stat(keepDir); err != nil {
		t.Errorf("a user directory was touched by session delete: %v", err)
	}
}

// The startup sweep trashes task workspaces whose session no longer exists,
// and leaves live ones alone. Idempotent.
func TestTaskWorkspace_OrphanSweep(t *testing.T) {
	srv := groupTestServer(t)
	live := agent.NewSession("m", "")
	if err := live.Save(); err != nil {
		t.Fatal(err)
	}
	srv.applyDefaultWorkspaceDir(live)

	orphan := filepath.Join(srv.curWorkspaceDir(), "tasks", "20250101-000000-00000000")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}

	srv.sweepOrphanTaskWorkspaces()
	srv.sweepOrphanTaskWorkspaces() // idempotent

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan task workspace survived the sweep: %v", err)
	}
	if _, err := os.Stat(live.WorkingDir); err != nil {
		t.Errorf("a live session's workspace was swept: %v", err)
	}
}
