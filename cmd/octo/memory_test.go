package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/memory"
	"github.com/open-octo/octo-agent/internal/server"
)

// `octo memory path <dir>` resolves memory for somewhere other than cwd —
// how the agent finds ANOTHER project's memory dir when a durable fact belongs
// there. Resolution goes through the registry: the directory's claiming
// project owns the notes, keyed on its ID, so what this prints is a directory
// some project actually reads.
func TestRunMemory_PathWithDirArg(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	project := t.TempDir()
	ref, err := server.EnsureProjectForDirOnly(project)
	if err != nil || ref.ID == "" {
		t.Fatalf("create project: %+v %v", ref, err)
	}
	want, err := ref.MemoryDir()
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runMemory([]string{"path", project}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, stderr.String())
	}
	got := strings.SplitN(strings.TrimSpace(stdout.String()), "\n", 2)[0]
	if got != want {
		t.Errorf("path %q = %q, want the claiming project's %q", project, got, want)
	}
}

// A directory no project references resolves to the shared tier — inventing a
// path-derived directory nothing reads anymore would send notes into a black
// hole.
func TestRunMemory_PathWithNonRepoDirArg(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	scratch := filepath.Join(home, "notes")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	shared, err := memory.HomeDir()
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runMemory([]string{"path", scratch}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, stderr.String())
	}
	got := strings.SplitN(strings.TrimSpace(stdout.String()), "\n", 2)[0]
	if got != shared {
		t.Errorf("path %q = %q, want the shared tier %q", scratch, got, shared)
	}
}

// The home directory is the one place that lands on the shared tier, and not by
// a special case: its own slug directory IS that tier.
func TestRunMemory_PathInHomeIsSharedDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	want, err := memory.HomeDir()
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runMemory([]string{"path", home}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, stderr.String())
	}
	got := strings.SplitN(strings.TrimSpace(stdout.String()), "\n", 2)[0]
	if got != want {
		t.Errorf("path %q = %q, want the shared dir %q", home, got, want)
	}
}

// The CLI's write-allowlist roots: the whole memories tree when memory is on,
// nothing at all when it's off (--no-memory leaves memDir empty). Mirrors the
// server's TestMemoryWriteRoots_*.
func TestMemoryWriteRoots_CLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	root, err := memory.RootDir()
	if err != nil {
		t.Fatal(err)
	}
	got := memoryWriteRoots(filepath.Join(root, "proj-1234abcd"), filepath.Join(root, "home-5678ef01"))
	if len(got) != 1 || got[0] != root {
		t.Errorf("roots = %v, want [%s]", got, root)
	}

	// --no-memory (or an unresolvable/uncreatable dir) → no standing write pass.
	if got := memoryWriteRoots("", ""); len(got) != 0 {
		t.Errorf("roots = %v, want none when memory is disabled", got)
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
