package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/trash"
)

func trashTestHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

// stage writes a file in project and moves it to the trash, returning its
// original path.
func stage(t *testing.T, project, name, content string) string {
	t.Helper()
	p := filepath.Join(project, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := trash.Move(p, project); err != nil {
		t.Fatal(err)
	}
	return p
}

func rt(args ...string) (string, int) {
	var b bytes.Buffer
	code := runTrash(args, &b, &b)
	return b.String(), code
}

func TestTrashCLI_ListEmpty(t *testing.T) {
	trashTestHome(t)
	out, code := rt("list")
	if code != 0 || !strings.Contains(out, "empty") {
		t.Fatalf("list empty: code=%d out=%q", code, out)
	}
}

func TestTrashCLI_ListShowsEntries(t *testing.T) {
	trashTestHome(t)
	project := t.TempDir()
	stage(t, project, "notes.txt", "hi")
	out, code := rt("list")
	if code != 0 || !strings.Contains(out, "notes.txt") {
		t.Fatalf("list: code=%d out=%q", code, out)
	}
}

func TestTrashCLI_RestoreBySubstring(t *testing.T) {
	trashTestHome(t)
	project := t.TempDir()
	orig := stage(t, project, "notes.txt", "hi")

	out, code := rt("restore", "notes")
	if code != 0 || !strings.Contains(out, "Restored to") {
		t.Fatalf("restore: code=%d out=%q", code, out)
	}
	if got, _ := os.ReadFile(orig); string(got) != "hi" {
		t.Fatalf("restored content = %q", got)
	}
}

func TestTrashCLI_RestoreConflictNeedsFlag(t *testing.T) {
	trashTestHome(t)
	project := t.TempDir()
	orig := stage(t, project, "notes.txt", "old")
	// Recreate a new file at the original path.
	if err := os.WriteFile(orig, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := rt("restore", "notes")
	if code == 0 || !strings.Contains(out, "already exists") {
		t.Fatalf("expected conflict message + nonzero exit, got code=%d out=%q", code, out)
	}
	if got, _ := os.ReadFile(orig); string(got) != "new" {
		t.Fatalf("current file must be untouched on abort, got %q", got)
	}

	// --overwrite resolves it (current file goes to trash, old one restored).
	out, code = rt("restore", "notes", "--overwrite")
	if code != 0 {
		t.Fatalf("restore --overwrite: code=%d out=%q", code, out)
	}
	if got, _ := os.ReadFile(orig); string(got) != "old" {
		t.Fatalf("after --overwrite content = %q, want old", got)
	}
}

func TestTrashCLI_RmBySubstring(t *testing.T) {
	trashTestHome(t)
	project := t.TempDir()
	stage(t, project, "gone.txt", "bye")
	out, code := rt("rm", "gone")
	if code != 0 || !strings.Contains(out, "Permanently deleted") {
		t.Fatalf("rm: code=%d out=%q", code, out)
	}
	entries, _ := trash.List()
	if len(entries) != 0 {
		t.Fatalf("entry should be gone, %d remain", len(entries))
	}
}

func TestTrashCLI_AmbiguousSubstringListsCandidates(t *testing.T) {
	trashTestHome(t)
	project := t.TempDir()
	stage(t, project, "config-a.json", "a")
	stage(t, project, "config-b.json", "b")
	out, code := rt("restore", "config")
	if code == 0 || !strings.Contains(out, "matches 2 entries") {
		t.Fatalf("ambiguous: code=%d out=%q", code, out)
	}
}

func TestTrashCLI_EmptyRequiresMode(t *testing.T) {
	trashTestHome(t)
	if _, code := rt("empty"); code != 2 {
		t.Fatalf("empty with no mode should exit 2, got %d", code)
	}
	project := t.TempDir()
	stage(t, project, "x.txt", "x")
	out, code := rt("empty", "--all")
	if code != 0 || !strings.Contains(out, "Removed 1") {
		t.Fatalf("empty --all: code=%d out=%q", code, out)
	}
}

func TestTrashCLI_UnknownSubcommand(t *testing.T) {
	trashTestHome(t)
	if _, code := rt("bogus"); code != 2 {
		t.Fatalf("unknown subcommand should exit 2, got %d", code)
	}
}
