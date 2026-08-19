package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/memory"
)

// projectFor returns the project owning sessionID, or nil.
func projectFor(t *testing.T, sessionID string) *sessionGroup {
	t.Helper()
	return projectForSession(sessionID)
}

// saveSessionIn persists a session whose own working directory is dir.
func saveSessionIn(t *testing.T, dir string) *agent.Session {
	t.Helper()
	sess := agent.NewSession("stub-model", "")
	sess.WorkingDir = dir
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	return sess
}

// A session that chose a directory becomes a member of a project for it — the
// directory it was already working in, now attached to the thing that also
// scopes its memory.
func TestAdoptTaskWorkingDirs_ChosenDirectoryBecomesAProject(t *testing.T) {
	setTestHome(t)
	dir := t.TempDir()
	sess := saveSessionIn(t, dir)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.adoptTaskWorkingDirs()

	p := projectFor(t, sess.ID)
	if p == nil {
		t.Fatal("session was not adopted into a project")
	}
	if memory.NormalizeDir(p.WorkingDir) != memory.NormalizeDir(dir) {
		t.Errorf("project working dir = %q, want %q", p.WorkingDir, dir)
	}
	if p.Name != filepath.Base(memory.NormalizeDir(dir)) {
		t.Errorf("project name = %q, want the directory's basename %q", p.Name, filepath.Base(dir))
	}
}

// The case that makes the exclusion non-negotiable: every session that never
// chose a directory carries the workspace, because applyDefaultWorkspaceDir
// seeds it. Adopting those would sweep the entire task list into one project
// named after the workspace — destroying the very distinction this honours.
func TestAdoptTaskWorkingDirs_LeavesTheWorkspaceAlone(t *testing.T) {
	setTestHome(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(home, "Octo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	sess := saveSessionIn(t, workspace)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.workspaceDir = workspace
	srv.adoptTaskWorkingDirs()

	if p := projectFor(t, sess.ID); p != nil {
		t.Errorf("the workspace was adopted into project %q; it is a default, not a choice", p.Name)
	}
}

// ~/Octo is excluded whether or not it is the configured workspace. A machine
// whose workspace_dir was changed later — or whose sessions came from a backup —
// still has ~/Octo in everything written before the change, and comparing only
// against the live setting would sweep exactly those into a project. This is the
// case a dry run against real data caught.
func TestAdoptTaskWorkingDirs_ExcludesTheBuiltinDefaultUnconditionally(t *testing.T) {
	setTestHome(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	builtin := filepath.Join(home, "Octo")
	if err := os.MkdirAll(builtin, 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()

	inBuiltin := saveSessionIn(t, builtin)
	inChosen := saveSessionIn(t, elsewhere)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	// The configured workspace has since been moved somewhere else entirely.
	srv.workspaceDir = t.TempDir()
	srv.adoptTaskWorkingDirs()

	if p := projectFor(t, inBuiltin.ID); p != nil {
		t.Errorf("~/Octo was adopted into project %q despite no longer being the configured workspace", p.Name)
	}
	// …while a directory that really was chosen is still adopted.
	if p := projectFor(t, inChosen.ID); p == nil {
		t.Error("a chosen directory should still become a project")
	}
}

// Everything that is not the workspace counts as a choice — including the home
// directory, which some sessions were pointed at deliberately.
func TestAdoptTaskWorkingDirs_AdoptsAnythingElse(t *testing.T) {
	setTestHome(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	sess := saveSessionIn(t, home)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.adoptTaskWorkingDirs()

	if p := projectFor(t, sess.ID); p == nil {
		t.Error("home was not adopted; only the workspace is excluded")
	}
}

// Several sessions in one directory share one project, not one each.
func TestAdoptTaskWorkingDirs_OneProjectPerDirectory(t *testing.T) {
	setTestHome(t)
	dir := t.TempDir()
	a := saveSessionIn(t, dir)
	b := saveSessionIn(t, dir)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.adoptTaskWorkingDirs()

	pa, pb := projectFor(t, a.ID), projectFor(t, b.ID)
	if pa == nil || pb == nil {
		t.Fatal("both sessions should have been adopted")
	}
	if pa.ID != pb.ID {
		t.Errorf("two sessions in one directory got two projects: %q and %q", pa.ID, pb.ID)
	}
}

// Running twice must not double up: the second pass finds the project the first
// one made, which is what lets this run on every start.
func TestAdoptTaskWorkingDirs_Idempotent(t *testing.T) {
	setTestHome(t)
	dir := t.TempDir()
	sess := saveSessionIn(t, dir)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.adoptTaskWorkingDirs()
	first := projectFor(t, sess.ID)
	if first == nil {
		t.Fatal("not adopted on the first pass")
	}

	srv.adoptTaskWorkingDirs()

	groupMu.Lock()
	groups, err := loadSessionGroups()
	groupMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for i := range groups {
		if groups[i].WorkingDir != "" && memory.NormalizeDir(groups[i].WorkingDir) == memory.NormalizeDir(dir) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d projects for one directory after two passes, want 1", n)
	}
	// And the session is filed once, not twice.
	p := projectFor(t, sess.ID)
	if p == nil {
		t.Fatal("session lost its project on the second pass")
	}
	count := 0
	for _, id := range p.SessionIDs {
		if id == sess.ID {
			count++
		}
	}
	if count != 1 {
		t.Errorf("session listed %d times in its project, want 1", count)
	}
}

// A session already in a project is left where it is, even if its own stale
// WorkingDir points elsewhere — the project is the authority.
func TestAdoptTaskWorkingDirs_SkipsSessionsAlreadyInAProject(t *testing.T) {
	setTestHome(t)
	projectDir := t.TempDir()
	staleOwnDir := t.TempDir()

	sess := saveSessionIn(t, staleOwnDir)
	g, err := createSessionGroupNamed("Existing", projectDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := addSessionToGroup(g.ID, sess.ID); err != nil {
		t.Fatal(err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.adoptTaskWorkingDirs()

	p := projectFor(t, sess.ID)
	if p == nil || p.ID != g.ID {
		t.Fatalf("session moved out of its project: %+v", p)
	}
}

// A directory that no longer exists cannot become a project (the group creation
// drops an unusable dir), so the session stays a task and keeps working.
func TestAdoptTaskWorkingDirs_SkipsAMissingDirectory(t *testing.T) {
	setTestHome(t)
	gone := filepath.Join(t.TempDir(), "deleted")
	sess := saveSessionIn(t, gone)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.adoptTaskWorkingDirs()

	if p := projectFor(t, sess.ID); p != nil {
		t.Errorf("adopted into %q despite the directory being gone", p.Name)
	}
	// No plain group left lying around either.
	groupMu.Lock()
	groups, err := loadSessionGroups()
	groupMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for i := range groups {
		if len(groups[i].SessionIDs) > 0 && groups[i].SessionIDs[0] == sess.ID {
			t.Errorf("session was filed under group %q with working dir %q", groups[i].Name, groups[i].WorkingDir)
		}
	}
}
