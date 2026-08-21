package server

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/open-octo/octo-agent/internal/memory"
	"github.com/open-octo/octo-agent/internal/tools"
)

// isolatedHome pins HOME to a temp dir so the registry these tests write is
// their own. Nothing here needs a Server: EnsureProjectForDir is the CLI's
// path, and the CLI has none.
func isolatedHome(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
}

// projectWithDir returns the group owning dir — as its workspace or as a
// mounted source folder — or nil.
func projectWithDir(t *testing.T, dir string) *sessionGroup {
	t.Helper()
	groups, err := loadSessionGroups()
	if err != nil {
		t.Fatalf("load groups: %v", err)
	}
	target := memory.NormalizeDir(dir)
	for i := range groups {
		if wd := groups[i].WorkingDir; wd != "" && memory.NormalizeDir(wd) == target {
			return &groups[i]
		}
		for _, sd := range groups[i].SourceDirs {
			if memory.NormalizeDir(sd) == target {
				return &groups[i]
			}
		}
	}
	return nil
}

// TestEnsureProject_CreatesAndFiles: a TUI session naming the directory it
// works in gets a project for that directory, named after it, with the session
// filed under it.
func TestEnsureProject_CreatesAndFiles(t *testing.T) {
	isolatedHome(t)
	dir := filepath.Join(t.TempDir(), "my-repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := EnsureProjectForDir(dir, "sess-1"); err != nil {
		t.Fatalf("EnsureProjectForDir: %v", err)
	}

	g := projectWithDir(t, dir)
	if g == nil {
		t.Fatalf("no project created for %s", dir)
	}
	if g.Name != "my-repo" {
		t.Errorf("project name = %q, want %q", g.Name, "my-repo")
	}
	if len(g.SessionIDs) != 1 || g.SessionIDs[0] != "sess-1" {
		t.Errorf("session ids = %v, want [sess-1]", g.SessionIDs)
	}
	// The user's directory is mounted, not taken as the project directory:
	// the workspace is generated under the workspace root.
	if len(g.SourceDirs) != 1 || g.SourceDirs[0] != dir {
		t.Errorf("source dirs = %v, want [%s]", g.SourceDirs, dir)
	}
	if g.WorkingDir == dir || g.WorkingDir == "" {
		t.Errorf("working dir = %q, want a generated workspace distinct from %s", g.WorkingDir, dir)
	}
	if root, err := tools.ResolveWorkspaceDir(""); err == nil {
		if !strings.HasPrefix(g.WorkingDir, root+string(filepath.Separator)) {
			t.Errorf("workspace %q is not under the workspace root %q", g.WorkingDir, root)
		}
	}
}

// TestEnsureProject_ReusesProjectForSameDir: a second session started in the
// same directory joins the project already there rather than making a second
// one — the match is on the directory, not the name.
func TestEnsureProject_ReusesProjectForSameDir(t *testing.T) {
	isolatedHome(t)
	dir := t.TempDir()

	for _, id := range []string{"sess-1", "sess-2"} {
		if err := EnsureProjectForDir(dir, id); err != nil {
			t.Fatalf("EnsureProjectForDir(%s): %v", id, err)
		}
	}

	groups, err := loadSessionGroups()
	if err != nil {
		t.Fatalf("load groups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1: %+v", len(groups), groups)
	}
	if len(groups[0].SessionIDs) != 2 {
		t.Errorf("session ids = %v, want both sessions", groups[0].SessionIDs)
	}
}

// TestEnsureProject_SkipsWorkspaceDir: the workspace directory means "nobody
// chose this", so adopting it would file every default-accepting session under
// one project named after the workspace.
func TestEnsureProject_SkipsWorkspaceDir(t *testing.T) {
	isolatedHome(t)
	workspace, err := tools.ResolveWorkspaceDir("")
	if err != nil {
		t.Skipf("cannot resolve the built-in workspace dir: %v", err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	if err := EnsureProjectForDir(workspace, "sess-1"); err != nil {
		t.Fatalf("EnsureProjectForDir: %v", err)
	}

	groups, err := loadSessionGroups()
	if err != nil {
		t.Fatalf("load groups: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("workspace dir was adopted into %+v, want no project", groups)
	}
}

// TestEnsureProject_JoinsAWorkspaceProjectTheUserMade: no project is created
// for the workspace, but one the user built there by hand is joined — otherwise
// terminal sessions would sit outside a project that the web sessions in the
// very same directory are in.
func TestEnsureProject_JoinsAWorkspaceProjectTheUserMade(t *testing.T) {
	isolatedHome(t)
	workspace, err := tools.ResolveWorkspaceDir("")
	if err != nil {
		t.Skipf("cannot resolve the built-in workspace dir: %v", err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	// Nothing there yet: the workspace does not become a project on its own.
	if err := EnsureProjectForDir(workspace, "sess-before"); err != nil {
		t.Fatalf("EnsureProjectForDir before: %v", err)
	}
	if g := projectWithDir(t, workspace); g != nil {
		t.Fatalf("workspace became a project on its own: %+v", g)
	}

	// The user makes one deliberately (what the Web UI's "new project" does).
	if _, err := createSessionGroupNamed("My workspace", workspace, ""); err != nil {
		t.Fatalf("create the project: %v", err)
	}
	if err := EnsureProjectForDir(workspace, "sess-after"); err != nil {
		t.Fatalf("EnsureProjectForDir after: %v", err)
	}

	g := projectWithDir(t, workspace)
	if g == nil {
		t.Fatal("the project the user made is gone")
	}
	found := false
	for _, id := range g.SessionIDs {
		if id == "sess-after" {
			found = true
		}
		if id == "sess-before" {
			t.Error("a session from before the project existed was filed into it retroactively")
		}
	}
	if !found {
		t.Errorf("session was not filed into the user's workspace project: %v", g.SessionIDs)
	}
}

// TestEnsureProject_LeavesSessionAlreadyInProject: idempotent enough to call on
// every session start — one already filed somewhere is not moved.
func TestEnsureProject_LeavesSessionAlreadyInProject(t *testing.T) {
	isolatedHome(t)
	first, second := t.TempDir(), t.TempDir()

	if err := EnsureProjectForDir(first, "sess-1"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := EnsureProjectForDir(second, "sess-1"); err != nil {
		t.Fatalf("second: %v", err)
	}

	if g := projectWithDir(t, first); g == nil || len(g.SessionIDs) != 1 {
		t.Errorf("session left its original project: %+v", g)
	}
	if g := projectWithDir(t, second); g != nil {
		t.Errorf("second directory got a project anyway: %+v", g)
	}
}

// TestEnsureProject_MissingDirIsRefused: a directory that no longer validates
// gets no group at all — a group without a directory answers none of the
// questions the directory did.
func TestEnsureProject_MissingDirIsRefused(t *testing.T) {
	isolatedHome(t)
	gone := filepath.Join(t.TempDir(), "not-there")

	if err := EnsureProjectForDir(gone, "sess-1"); err == nil {
		t.Fatal("EnsureProjectForDir on a missing directory: want error, got nil")
	}
	groups, err := loadSessionGroups()
	if err != nil {
		t.Fatalf("load groups: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("groups = %+v, want none", groups)
	}
}

// TestEnsureProject_ConcurrentWritesKeepBoth: the registry is one file
// rewritten whole, so interleaved read-modify-write cycles would lose a group.
// This covers the in-process half (the mutex); the cross-process half has its
// own test below.
func TestEnsureProject_ConcurrentWritesKeepBoth(t *testing.T) {
	isolatedHome(t)
	dirs := make([]string, 8)
	for i := range dirs {
		dirs[i] = t.TempDir()
	}

	var wg sync.WaitGroup
	errs := make([]error, len(dirs))
	for i, dir := range dirs {
		wg.Add(1)
		go func(i int, dir string) {
			defer wg.Done()
			errs[i] = EnsureProjectForDir(dir, "sess-"+filepath.Base(dir))
		}(i, dir)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("EnsureProjectForDir(%s): %v", dirs[i], err)
		}
	}
	for _, dir := range dirs {
		if g := projectWithDir(t, dir); g == nil {
			t.Errorf("project for %s was lost to a concurrent write", dir)
		}
	}
}
