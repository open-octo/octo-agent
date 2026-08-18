package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/memory"
)

// `octo memory path <dir>` resolves memory for somewhere other than cwd —
// how the agent finds ANOTHER repo's memory dir when a durable fact belongs
// there. One command, so it works on shells without && chaining.
func TestRunMemory_PathWithDirArg(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init").CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %v (%s)", err, out)
	}
	want, err := memory.Dir(memory.ProjectRoot(repo))
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runMemory([]string{"path", repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, stderr.String())
	}
	got := strings.SplitN(strings.TrimSpace(stdout.String()), "\n", 2)[0]
	if got != want {
		t.Errorf("path %q = %q, want %q", repo, got, want)
	}
}

// A non-repo target resolves to the shared dir, not a slug of its own.
func TestRunMemory_PathWithNonRepoDirArg(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	scratch := filepath.Join(home, "Octo")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	want, err := memory.HomeDir()
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runMemory([]string{"path", scratch}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != want {
		t.Errorf("path %q = %q, want shared dir %q", scratch, got, want)
	}
}

// Notes written before non-repo dirs shared the global tier sit in a slug dir
// for that path and are no longer injected. `octo memory list` must name that
// directory instead of orphaning it silently.
func TestRunMemory_ListNamesOrphanedSlugDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	scratch := filepath.Join(home, "Octo")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	// Seed what an older octo would have written for this non-repo directory.
	legacy, err := memory.Dir(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "MEMORY.md"), []byte("- old note\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Target the directory by argument rather than os.Chdir — the cwd is
	// process-global and would leak into every other test in this package.
	var stdout, stderr bytes.Buffer
	if code := runMemory([]string{"list", scratch}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, legacy) {
		t.Errorf("list output does not name the orphaned dir %q:\n%s", legacy, out)
	}
	if !strings.Contains(out, "no longer loaded") {
		t.Errorf("list output does not explain the orphaned notes:\n%s", out)
	}

	// An empty legacy dir is not worth mentioning.
	if err := os.Remove(filepath.Join(legacy, "MEMORY.md")); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := runMemory([]string{"list", scratch}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(stdout.String(), "no longer loaded") {
		t.Error("an empty legacy dir should not be reported")
	}
}

func TestRunMemory_RejectsNonDirArg(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	file := filepath.Join(home, "notadir.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runMemory([]string{"path", file}, &stdout, &stderr); code == 0 {
		t.Errorf("exit = 0 for a file argument, want non-zero (stdout: %s)", stdout.String())
	}
	if !strings.Contains(stderr.String(), "not a directory") {
		t.Errorf("stderr = %q, want a 'not a directory' complaint", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runMemory([]string{"path", filepath.Join(home, "missing")}, &stdout, &stderr); code == 0 {
		t.Error("exit = 0 for a missing directory, want non-zero")
	}
}
