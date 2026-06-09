package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunTrashBackup backs the given paths up to the trash (project from
// OCTO_TRASH_PROJECT), leaves the originals in place, and always returns 0.
func TestRunTrashBackup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	project := t.TempDir()
	t.Setenv("OCTO_TRASH_PROJECT", project)
	f := filepath.Join(project, "keep.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// "--" and a bogus path are tolerated; the real path is backed up.
	if code := runTrashBackup([]string{"--", filepath.Join(project, "ghost"), f}); code != 0 {
		t.Errorf("runTrashBackup must always exit 0, got %d", code)
	}
	if _, err := os.Stat(f); err != nil {
		t.Errorf("backup must NOT delete the original: %v", err)
	}
	// The trash project dir should hold a .meta.json for the backed-up file.
	trashProj := filepath.Join(home, ".octo", "trash")
	var found bool
	_ = filepath.Walk(trashProj, func(p string, _ os.FileInfo, _ error) error {
		if strings.HasSuffix(p, ".meta.json") {
			found = true
		}
		return nil
	})
	if !found {
		t.Error("expected a .meta.json in the trash after backup")
	}
}
