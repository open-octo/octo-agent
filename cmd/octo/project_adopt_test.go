package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/server"
)

func adoptTestHome(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
}

// A cwd claimed by exactly one project joins it, with that project's memory.
func TestDecideProjectForCwd_UniqueClaimJoins(t *testing.T) {
	adoptTestHome(t)
	src := t.TempDir()
	ref, err := server.EnsureProjectForDirOnly(src)
	if err != nil || ref.ID == "" {
		t.Fatal(err)
	}
	wantMem, _ := ref.MemoryDir()

	d := decideProjectForCwd(src, false, bytes.NewReader(nil), &bytes.Buffer{})
	if d.ProjectID != ref.ID || d.MemDir != wantMem {
		t.Errorf("decision = %+v, want project %s mem %s", d, ref.ID, wantMem)
	}
}

// Several claimants headless: stay a task, say so, file nowhere. The
// two-claim registry is written directly — mounting a folder into a second
// project is a Web UI operation with no CLI surface.
func TestDecideProjectForCwd_AmbiguousHeadlessStaysTask(t *testing.T) {
	adoptTestHome(t)
	src := t.TempDir()
	home, _ := os.UserHomeDir()
	if err := os.MkdirAll(filepath.Join(home, ".octo"), 0o700); err != nil {
		t.Fatal(err)
	}
	registry := `{"groups":[` +
		`{"id":"g-aaa","name":"A","session_ids":[],"working_dir":"` + filepath.ToSlash(t.TempDir()) + `","source_dirs":["` + filepath.ToSlash(src) + `"]},` +
		`{"id":"g-bbb","name":"B","session_ids":[],"working_dir":"` + filepath.ToSlash(t.TempDir()) + `","source_dirs":["` + filepath.ToSlash(src) + `"]}]}`
	if err := os.WriteFile(filepath.Join(home, ".octo", "session-groups.json"), []byte(registry), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	d := decideProjectForCwd(src, false, bytes.NewReader(nil), &out)
	if d.ProjectID != "" || d.MemDir != "" {
		t.Errorf("headless ambiguity must stay a task, got %+v", d)
	}
	if d.Note == "" {
		t.Error("the ambiguity must be said out loud, not silent")
	}
}

// An unclaimed git repository becomes a project at startup; a plain directory
// stays a task with no project memory.
func TestDecideProjectForCwd_UnclaimedRepoCreatesProject(t *testing.T) {
	adoptTestHome(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "internal", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	d := decideProjectForCwd(sub, false, bytes.NewReader(nil), &bytes.Buffer{})
	if d.ProjectID == "" || d.MemDir == "" {
		t.Fatalf("repo subdir should create+join a project, got %+v", d)
	}
	claims := server.ProjectsClaimingDir(sub)
	if len(claims) != 1 || claims[0].ID != d.ProjectID {
		t.Errorf("claims after create = %+v", claims)
	}

	plain := t.TempDir()
	d = decideProjectForCwd(plain, false, bytes.NewReader(nil), &bytes.Buffer{})
	if d.ProjectID != "" || d.MemDir != "" {
		t.Errorf("a plain directory must stay a task, got %+v", d)
	}
	if got := server.ProjectsClaimingDir(plain); len(got) != 0 {
		t.Errorf("a project row appeared for a plain directory: %+v", got)
	}
}

func TestInsideGitRepo(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: elsewhere"), 0o600); err != nil {
		t.Fatal(err) // a worktree-style .git FILE must count too
	}
	if !insideGitRepo(repo) {
		t.Error(".git file not detected")
	}
	if insideGitRepo(t.TempDir()) {
		t.Error("plain temp dir detected as a repo")
	}
}
