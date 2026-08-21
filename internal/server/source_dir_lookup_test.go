package server

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/memory"
	"github.com/open-octo/octo-agent/internal/tools"
)

func TestProjectsClaimingDir_MatchesWorkspaceAndSourceDirs(t *testing.T) {
	isolatedHome(t)
	src := t.TempDir()

	a, err := EnsureProjectForDirOnly(src)
	if err != nil || a.ID == "" {
		t.Fatalf("create project A: %+v %v", a, err)
	}

	// By source folder.
	claims := ProjectsClaimingDir(src)
	if len(claims) != 1 || claims[0].ID != a.ID {
		t.Fatalf("claims for source dir = %+v, want [%s]", claims, a.ID)
	}
	// By workspace.
	claims = ProjectsClaimingDir(a.WorkspaceDir)
	if len(claims) != 1 || claims[0].ID != a.ID {
		t.Fatalf("claims for workspace = %+v, want [%s]", claims, a.ID)
	}
	// Unrelated dir: none.
	if got := ProjectsClaimingDir(t.TempDir()); len(got) != 0 {
		t.Fatalf("claims for unrelated dir = %+v, want none", got)
	}

	// A second project mounting the same folder → both claim it.
	gf, err := loadRegistryFile()
	if err != nil {
		t.Fatal(err)
	}
	gf.Groups = append(gf.Groups, sessionGroup{ID: "g-second", Name: "second", WorkingDir: t.TempDir(), SourceDirs: []string{src}})
	groupMu.LockWrite()
	err = saveRegistry(gf)
	groupMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if got := ProjectsClaimingDir(src); len(got) != 2 {
		t.Fatalf("claims after second mount = %+v, want 2", got)
	}
}

func TestEnsureProjectForDirOnly_SkipsWorkspaceDefaultsAndFilesNothing(t *testing.T) {
	isolatedHome(t)
	ws, err := tools.ResolveWorkspaceDir("")
	if err != nil {
		t.Skip("no built-in workspace")
	}
	ref, err := EnsureProjectForDirOnly(ws)
	if err != nil || ref.ID != "" {
		t.Fatalf("workspace default adopted: %+v %v", ref, err)
	}

	src := t.TempDir()
	ref, err = EnsureProjectForDirOnly(src)
	if err != nil || ref.ID == "" {
		t.Fatalf("EnsureProjectForDirOnly: %+v %v", ref, err)
	}
	if len(projectForSessionMapKeys(t)) != 0 {
		t.Fatal("a session was filed by a create-only call")
	}
	// Idempotent: same dir → same project.
	again, err := EnsureProjectForDirOnly(src)
	if err != nil || again.ID != ref.ID {
		t.Fatalf("second call = %+v, want the same project %s", again, ref.ID)
	}
}

// A scheduled task's run cluster never claims a directory: not in the
// read-only lookup, and not in find-or-create — the CLI's three-state rule
// checks claims first and falls through to EnsureProjectForDirOnly when a
// cron group is the only claimant, so both layers must agree or the session
// drops into the task's run history.
func TestProjectsClaimingDir_SkipsTaskGroups(t *testing.T) {
	isolatedHome(t)
	src := t.TempDir()

	gf, err := loadRegistryFile()
	if err != nil {
		t.Fatal(err)
	}
	cron := sessionGroup{ID: "g-cron", Name: "nightly", WorkingDir: t.TempDir(), SourceDirs: []string{src}, TaskID: "task-1"}
	gf.Groups = append(gf.Groups, cron)
	groupMu.LockWrite()
	err = saveRegistry(gf)
	groupMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	if got := ProjectsClaimingDir(src); len(got) != 0 {
		t.Fatalf("claims for a cron-mounted dir = %+v, want none", got)
	}
	if got := ProjectsClaimingDir(cron.WorkingDir); len(got) != 0 {
		t.Fatalf("claims for a cron workspace = %+v, want none", got)
	}

	// The find-or-create half: a fresh project mounting the folder, not the
	// cron cluster.
	ref, err := EnsureProjectForDirOnly(src)
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID == "" || ref.ID == cron.ID {
		t.Fatalf("EnsureProjectForDirOnly = %+v, want a NEW project, not the cron cluster", ref)
	}
	if got := ProjectsClaimingDir(src); len(got) != 1 || got[0].ID != ref.ID {
		t.Fatalf("claims after create = %+v, want [%s]", got, ref.ID)
	}
}

// projectForSessionMapKeys lists all session ids filed anywhere.
func projectForSessionMapKeys(t *testing.T) []string {
	t.Helper()
	groups, err := loadSessionGroups()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := range groups {
		ids = append(ids, groups[i].SessionIDs...)
	}
	return ids
}

func TestFileSessionInProject_And_MemoryDirForSession(t *testing.T) {
	isolatedHome(t)
	ref, err := EnsureProjectForDirOnly(t.TempDir())
	if err != nil || ref.ID == "" {
		t.Fatal(err)
	}
	if err := FileSessionInProject(ref.ID, "sess-1"); err != nil {
		t.Fatalf("FileSessionInProject: %v", err)
	}
	want, err := memory.DirForProjectID(ref.ID, filepath.Base(ref.WorkspaceDir))
	if err != nil {
		t.Fatal(err)
	}
	if got := ProjectMemoryDirForSession("sess-1"); got != want {
		t.Errorf("memory dir = %q, want %q", got, want)
	}
	if refDir, err := ref.MemoryDir(); err != nil || refDir != want {
		t.Errorf("ref.MemoryDir = %q, want %q", refDir, want)
	}
	if got := ProjectMemoryDirForSession("sess-none"); got != "" {
		t.Errorf("sessionless lookup = %q, want empty", got)
	}
	if err := FileSessionInProject("g-missing", "sess-2"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("filing into a missing project: err = %v, want not-found", err)
	}
}
