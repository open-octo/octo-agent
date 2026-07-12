package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/trash"
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

// TestRunTrashBackup_RecordsRmProvenance covers the Go half of the Windows
// safe-delete path (the PowerShell wrapper shells out to `octo __trash-backup`):
// the backup must be recoverable AND stamped with rm/delete provenance, for
// both a relative and an absolute path argument. This runs on every platform,
// so Windows CI exercises it too.
func TestRunTrashBackup_RecordsRmProvenance(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	project := t.TempDir()
	t.Setenv("OCTO_TRASH_PROJECT", project)
	rel := filepath.Join(project, "rel.txt")
	abs := filepath.Join(t.TempDir(), "abs.txt") // outside the project dir
	for _, p := range []string{rel, abs} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if code := runTrashBackup([]string{"--", rel, abs}); code != 0 {
		t.Fatalf("runTrashBackup exit = %d, want 0", code)
	}

	entries, err := trash.List()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]trash.Entry{}
	for _, e := range entries {
		got[e.Original] = e
	}
	for _, p := range []string{rel, abs} {
		e, ok := got[p]
		if !ok {
			t.Errorf("no trash entry for %s", p)
			continue
		}
		if e.DeletedBy != "rm" || e.Kind != "delete" {
			t.Errorf("%s: provenance = %q/%q, want rm/delete", p, e.DeletedBy, e.Kind)
		}
	}
}

// TestRunTrashBackup_ProjectFallsBackToCWD: with OCTO_TRASH_PROJECT unset the
// backup still happens, keyed to the working directory.
func TestRunTrashBackup_ProjectFallsBackToCWD(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("OCTO_TRASH_PROJECT", "")

	project := t.TempDir()
	f := filepath.Join(project, "keep.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runTrashBackup([]string{f}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	entries, _ := trash.List()
	if len(entries) != 1 || entries[0].Original != f {
		t.Fatalf("expected the file backed up under the cwd project, got %+v", entries)
	}
}
