package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestFileTools_HonorWorkingDir verifies the worktree-isolation prerequisite:
// a relative path passed to write_file/read_file resolves against
// WorkingDir(ctx), not the process CWD. Without this a worktree-isolated
// sub-agent's file writes would leak into the main checkout.
func TestFileTools_HonorWorkingDir(t *testing.T) {
	dir := t.TempDir()
	ctx := WithWorkingDir(context.Background(), dir)

	if _, err := (WriteFileTool{}).Execute(ctx, "write_file", map[string]any{
		"path": "sub/rel.txt", "content": "hello",
	}); err != nil {
		t.Fatalf("write_file: %v", err)
	}

	// It must land under the working dir, not the process CWD.
	want := filepath.Join(dir, "sub", "rel.txt")
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("expected file at %s: %v", want, err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want hello", string(data))
	}

	// And read_file with the same relative path + ctx reads it back.
	res, err := (ReadFileTool{}).Execute(ctx, "read_file", map[string]any{"path": "sub/rel.txt"})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if res.Text == "" {
		t.Error("read_file returned empty for the working-dir-relative path")
	}
}

// printCwdCommand returns a shell command that prints the shell's current
// working directory on the platform shell shellCommand selects (POSIX sh vs
// PowerShell).
func printCwdCommand() string {
	if runtime.GOOS == "windows" {
		return "(Get-Location).Path"
	}
	return "pwd"
}

// sameDir compares two directory paths after symlink resolution (macOS
// t.TempDir lives under /var/folders → /private/var/folders).
func sameDir(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	return ra == rb
}

// TestTerminalTool_SyncHonorsWorkingDir is the terminal-side counterpart of
// TestFileTools_HonorWorkingDir — and the regression guard for the field bug
// where every serve-side shell command ran in the server process's cwd: the
// synchronous path routes through BackgroundManager.Start, which used to
// build its shell from a bare context.Background(), dropping the
// WithWorkingDir stamp that buildAgent/prepareToolTurn thread through the
// turn ctx.
func TestTerminalTool_SyncHonorsWorkingDir(t *testing.T) {
	dir := t.TempDir()
	ctx := WithWorkingDir(context.Background(), dir)

	res, err := TerminalTool{mgr: NewBackgroundManager()}.Execute(ctx, "terminal", map[string]any{
		"command": printCwdCommand(),
	})
	if err != nil {
		t.Fatalf("terminal: %v", err)
	}
	got := strings.TrimSpace(res.Text)
	if !sameDir(got, dir) {
		procCwd, _ := os.Getwd()
		t.Errorf("sync terminal ran in %q, want the ctx working dir %q (process cwd %q)", got, dir, procCwd)
	}
}

// Background (async/interactive) launches must honor the stamp too — they
// share BackgroundManager.Start with the sync path. Only the working-dir
// VALUE crosses; lifecycle stays detached from the turn ctx (asserted by
// TestBackgroundServerLifecycle and friends).
func TestBackgroundManager_StartHonorsCtxWorkingDir(t *testing.T) {
	dir := t.TempDir()
	ctx := WithWorkingDir(context.Background(), dir)

	m := NewBackgroundManager()
	id, err := m.Start(ctx, printCwdCommand(), BgModeAsync)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	var out string
	waitFor(t, "process to exit", func() bool {
		o, s, found, _, _ := m.Read(id)
		out += o
		return found && strings.HasPrefix(s, "exited")
	})
	got := strings.TrimSpace(out)
	if !sameDir(got, dir) {
		procCwd, _ := os.Getwd()
		t.Errorf("background command ran in %q, want the ctx working dir %q (process cwd %q)", got, dir, procCwd)
	}
}
