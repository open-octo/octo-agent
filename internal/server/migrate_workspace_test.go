package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/memory"
)

// A pre-workspace project — WorkingDir pointing at the user's directory —
// migrates to a generated workspace with the old directory mounted, its
// memory moved to the ID slug, and its dirless member sessions kept running
// exactly where they ran (write-back + inverted precedence).
func TestMigrateWorkspaces_LegacyProject(t *testing.T) {
	srv := groupTestServer(t)
	oldDir := t.TempDir()

	// Legacy registry shape: the user's directory IS the project directory.
	sess := agent.NewSession("m", "")
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	groupMu.LockWrite()
	err := saveRegistry(groupFile{Groups: []sessionGroup{{ID: "g-legacy", Name: "Work", SessionIDs: []string{sess.ID}, WorkingDir: oldDir}}})
	groupMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// Legacy memory under the path slug.
	oldMem, err := memory.Dir(oldDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(oldMem, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldMem, "MEMORY.md"), []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv.migrateProjectWorkspaces()
	srv.migrateProjectWorkspaces() // idempotent

	groups, err := loadSessionGroups()
	if err != nil || len(groups) != 1 {
		t.Fatalf("groups after migration: %v %v", groups, err)
	}
	g := groups[0]
	root := srv.curWorkspaceDir()
	if !strings.HasPrefix(g.WorkingDir, root+string(filepath.Separator)) {
		t.Errorf("WorkingDir = %q, want a workspace under %q", g.WorkingDir, root)
	}
	if len(g.SourceDirs) != 1 || g.SourceDirs[0] != oldDir {
		t.Errorf("old directory not mounted: %v", g.SourceDirs)
	}

	// Memory followed the project ID; the old slug is gone; content intact.
	newMem, err := memory.DirForProjectID(g.ID, filepath.Base(g.WorkingDir))
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(newMem, "MEMORY.md")); err != nil || string(b) != "notes" {
		t.Errorf("memory did not move intact: %v %q", err, b)
	}
	if _, err := os.Stat(oldMem); !os.IsNotExist(err) {
		t.Errorf("old memory slug still present")
	}

	// The dirless member session got the OLD directory written back, so it
	// keeps running where it always did.
	reloaded, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.WorkingDir != oldDir {
		t.Errorf("member session dir = %q, want the old project dir %q written back", reloaded.WorkingDir, oldDir)
	}
	if got := srv.sessionCwd(reloaded); got != oldDir {
		t.Errorf("member session cwd = %q, want %q (zero behavior change)", got, oldDir)
	}
}

// The one directory whose slug must never move: a project that pointed at
// $HOME shares its slug with the home tier every session reads.
func TestMigrateWorkspaces_NeverMovesTheSharedTier(t *testing.T) {
	srv := groupTestServer(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	shared, err := memory.HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "MEMORY.md"), []byte("shared"), 0o600); err != nil {
		t.Fatal(err)
	}
	groupMu.LockWrite()
	err = saveRegistry(groupFile{Groups: []sessionGroup{{ID: "g-home", Name: "Home", SessionIDs: []string{}, WorkingDir: home}}})
	groupMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	srv.migrateProjectWorkspaces()

	if _, err := os.Stat(filepath.Join(shared, "MEMORY.md")); err != nil {
		t.Errorf("the shared tier was moved: %v", err)
	}
}

// A cron project written before the workspace model, with an explicit user
// directory, keeps that directory as its output mount.
func TestMigrateWorkspaces_LegacyCronDirBecomesOutputMount(t *testing.T) {
	srv := groupTestServer(t)
	dir := t.TempDir()
	groupMu.LockWrite()
	err := saveRegistry(groupFile{Groups: []sessionGroup{{ID: "g-cron", Name: "日报", SessionIDs: []string{}, WorkingDir: dir, TaskID: "task-1"}}})
	groupMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	srv.migrateProjectWorkspaces()

	groups, _ := loadSessionGroups()
	if len(groups) != 1 {
		t.Fatal("group lost")
	}
	g := groups[0]
	if g.OutputDir != dir || len(g.SourceDirs) != 1 || g.SourceDirs[0] != dir {
		t.Errorf("legacy cron dir not an output mount: %+v", g)
	}
	if g.WorkingDir == dir {
		t.Errorf("legacy cron dir adopted as workspace itself")
	}
}
