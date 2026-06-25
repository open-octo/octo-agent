package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initGitRepo makes a temp git repo with one commit (worktree add needs a HEAD)
// and chdirs into it for the duration of the test (newWorktree resolves the
// repo from the process cwd).
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := gitRun(dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitRun(dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitRun(dir, "commit", "-q", "-m", "init"); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	// Resolve symlinks (macOS /var → /private/var) so path comparisons hold.
	real, _ := gitRun(dir, "rev-parse", "--show-toplevel")
	return real
}

func TestWorktree_ChangedLeavesBranch(t *testing.T) {
	repo := initGitRepo(t)

	wt, err := newWorktree("My Agent")
	if err != nil {
		t.Fatalf("newWorktree: %v", err)
	}
	if !strings.HasPrefix(wt.branch, "octo-wf/my-agent-") {
		t.Errorf("branch = %q, want octo-wf/my-agent-… prefix", wt.branch)
	}
	if _, err := os.Stat(wt.dir); err != nil {
		t.Fatalf("worktree dir missing: %v", err)
	}

	// A change in the worktree must be committed onto the branch and reported.
	if err := os.WriteFile(filepath.Join(wt.dir, "new.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	note := wt.finish()
	if !strings.Contains(note, wt.branch) || !strings.Contains(note, "new.txt") {
		t.Errorf("finish note = %q, want it to mention the branch and new.txt", note)
	}
	// Branch exists and carries the commit.
	if out, _ := gitRun(repo, "branch", "--list", wt.branch); strings.TrimSpace(out) == "" {
		t.Errorf("branch %q should persist after a changed run", wt.branch)
	}
	if out, _ := gitRun(repo, "log", "--oneline", wt.branch); !strings.Contains(out, "octo worktree") {
		t.Errorf("branch should carry the worktree commit; log = %q", out)
	}
}

func TestWorktree_UnchangedCleansUp(t *testing.T) {
	repo := initGitRepo(t)

	wt, err := newWorktree("x")
	if err != nil {
		t.Fatalf("newWorktree: %v", err)
	}
	if note := wt.finish(); note != "" {
		t.Errorf("finish of unchanged run = %q, want empty (auto-cleaned)", note)
	}
	if _, err := os.Stat(wt.dir); !os.IsNotExist(err) {
		t.Errorf("worktree dir should be removed after an unchanged run")
	}
	if out, _ := gitRun(repo, "branch", "--list", wt.branch); strings.TrimSpace(out) != "" {
		t.Errorf("branch %q should be deleted after an unchanged run", wt.branch)
	}
}

func TestWorktree_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if _, err := newWorktree("x"); err == nil {
		t.Error("newWorktree outside a git repo should error")
	}
}
