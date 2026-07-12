package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/trash"
)

func setHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func countBackups(t *testing.T, orig string) int {
	t.Helper()
	entries, err := trash.List()
	if err != nil {
		t.Fatalf("trash.List: %v", err)
	}
	n := 0
	for _, e := range entries {
		if e.Original == orig {
			n++
		}
	}
	return n
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func gitCommitAll(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "x"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// TestOverwriteBackup_Untracked: a file with no git recovery path is staged
// before it's overwritten.
func TestOverwriteBackup_Untracked(t *testing.T) {
	setHome(t)
	dir := t.TempDir() // plain dir, not a git repo
	f := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(f, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	backupBeforeOverwrite(context.Background(), f)
	if got := countBackups(t, f); got != 1 {
		t.Errorf("untracked overwrite should stage 1 backup, got %d", got)
	}
}

// TestOverwriteBackup_TrackedClean: a committed, unmodified file is skipped —
// git already recovers it.
func TestOverwriteBackup_TrackedClean(t *testing.T) {
	setHome(t)
	dir := t.TempDir()
	initGitRepo(t, dir)
	f := filepath.Join(dir, "main.go")
	if err := os.WriteFile(f, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, dir)

	backupBeforeOverwrite(context.Background(), f)
	if got := countBackups(t, f); got != 0 {
		t.Errorf("clean tracked file should NOT be staged (git recovers it), got %d", got)
	}
}

// TestOverwriteBackup_TrackedDirty: a tracked file with uncommitted changes is
// staged — the working-tree bytes differ from anything git holds.
func TestOverwriteBackup_TrackedDirty(t *testing.T) {
	setHome(t)
	dir := t.TempDir()
	initGitRepo(t, dir)
	f := filepath.Join(dir, "main.go")
	if err := os.WriteFile(f, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, dir)
	if err := os.WriteFile(f, []byte("package main // edited"), 0o644); err != nil {
		t.Fatal(err)
	}

	backupBeforeOverwrite(context.Background(), f)
	if got := countBackups(t, f); got != 1 {
		t.Errorf("dirty tracked file should be staged, got %d", got)
	}
}

// TestOverwriteBackup_CreateIsNoOp: no file at the path yet → nothing to stage.
func TestOverwriteBackup_CreateIsNoOp(t *testing.T) {
	setHome(t)
	dir := t.TempDir()
	f := filepath.Join(dir, "new.txt")
	backupBeforeOverwrite(context.Background(), f)
	if got := countBackups(t, f); got != 0 {
		t.Errorf("creating a new file should stage nothing, got %d", got)
	}
}

// TestOverwriteBackup_ToggleOff: trash.overwrite_backup:false disables staging
// even for an untracked file.
func TestOverwriteBackup_ToggleOff(t *testing.T) {
	setHome(t)
	home, _ := os.UserHomeDir()
	if err := os.MkdirAll(filepath.Join(home, ".octo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".octo", "config.yml"),
		[]byte("trash:\n  overwrite_backup: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(f, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	backupBeforeOverwrite(context.Background(), f)
	if got := countBackups(t, f); got != 0 {
		t.Errorf("toggle off should stage nothing, got %d", got)
	}
}
