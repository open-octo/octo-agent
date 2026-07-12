package trash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateHome points the trash root (os.UserHomeDir) at a temp dir so tests
// don't touch the real ~/.octo/trash.
func isolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)        // macOS/Linux
	t.Setenv("USERPROFILE", home) // Windows
}

func metaCount(t *testing.T, project string) int {
	t.Helper()
	entries, _ := os.ReadDir(ProjectDir(project))
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".meta.json") {
			n++
		}
	}
	return n
}

// TestBackup copies the file into the trash with a sidecar but leaves the
// original in place (the copy-only half used by the Windows delete wrapper).
func TestBackup(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	f := filepath.Join(project, "doomed.txt")
	if err := os.WriteFile(f, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Backup(f, project); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if _, err := os.Stat(f); err != nil {
		t.Errorf("Backup must NOT remove the original, but it's gone: %v", err)
	}
	if got := metaCount(t, project); got != 1 {
		t.Errorf("expected 1 trashed entry with meta, got %d", got)
	}
}

// TestMove backs up AND removes the original.
func TestMove(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	f := filepath.Join(project, "doomed.txt")
	if err := os.WriteFile(f, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Move(f, project); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Errorf("Move must remove the original, but it still exists (err=%v)", err)
	}
	if got := metaCount(t, project); got != 1 {
		t.Errorf("expected 1 trashed entry with meta, got %d", got)
	}
}

// TestBackup_MissingFile errors (and so the Windows wrapper ignores it).
func TestBackup_MissingFile(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	if _, err := Backup(filepath.Join(project, "nope"), project); err == nil {
		t.Error("Backup of a missing path should error")
	}
}
