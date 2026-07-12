package trash

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// find returns the single entry whose original path equals want.
func find(t *testing.T, want string) Entry {
	t.Helper()
	entries, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, e := range entries {
		if e.Original == want {
			return e
		}
	}
	t.Fatalf("no trash entry for %s (have %d)", want, len(entries))
	return Entry{}
}

// TestList_UniqueIDsAcrossProjects: two projects delete a file with the same
// basename; each entry gets a distinct ID and restores to its OWN original
// path — the old cross-project first-match-by-basename bug would restore the
// wrong file.
func TestList_UniqueIDsAcrossProjects(t *testing.T) {
	isolateHome(t)
	projA := t.TempDir()
	projB := t.TempDir()
	fa := filepath.Join(projA, "config.json")
	fb := filepath.Join(projB, "config.json")
	if err := os.WriteFile(fa, []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fb, []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Move(fa, projA); err != nil {
		t.Fatalf("Move A: %v", err)
	}
	if err := Move(fb, projB); err != nil {
		t.Fatalf("Move B: %v", err)
	}

	ea := find(t, fa)
	eb := find(t, fb)
	if ea.ID == eb.ID {
		t.Fatalf("IDs collided across projects: %q", ea.ID)
	}

	if _, err := Restore(ea.ID, ConflictAbort); err != nil {
		t.Fatalf("Restore A: %v", err)
	}
	got, err := os.ReadFile(fa)
	if err != nil || string(got) != "A" {
		t.Fatalf("project A restored wrong content: %q err=%v", got, err)
	}
	// B must be untouched by A's restore.
	if _, err := os.Stat(fb); !os.IsNotExist(err) {
		t.Fatalf("project B's file should still be in trash, not restored")
	}
}

func TestRestore_NoConflict(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	f := filepath.Join(project, "sub", "doomed.txt")
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Move(f, project); err != nil {
		t.Fatal(err)
	}
	// Remove the now-empty parent to prove Restore recreates it.
	os.RemoveAll(filepath.Dir(f))

	e := find(t, f)
	res, err := Restore(e.ID, ConflictAbort)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if res.RestoredTo != f {
		t.Errorf("RestoredTo = %q, want %q", res.RestoredTo, f)
	}
	got, err := os.ReadFile(f)
	if err != nil || string(got) != "data" {
		t.Fatalf("content mismatch: %q err=%v", got, err)
	}
}

func TestRestore_ConflictAbort(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	f := filepath.Join(project, "doomed.txt")
	if err := os.WriteFile(f, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Move(f, project); err != nil {
		t.Fatal(err)
	}
	// Recreate a NEW file at the original path.
	if err := os.WriteFile(f, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := find(t, f)
	_, err := Restore(e.ID, ConflictAbort)
	if !errors.Is(err, ErrRestoreConflict) {
		t.Fatalf("expected ErrRestoreConflict, got %v", err)
	}
	// The current file must be untouched — no silent overwrite.
	got, _ := os.ReadFile(f)
	if string(got) != "new" {
		t.Errorf("current file was clobbered: %q", got)
	}
}

func TestRestore_ConflictBackupExisting(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	f := filepath.Join(project, "doomed.txt")
	if err := os.WriteFile(f, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Move(f, project); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := find(t, f)
	res, err := Restore(e.ID, ConflictBackupExisting)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !res.BackedUpExisting {
		t.Error("BackedUpExisting should be true")
	}
	// The old version is back at the original path...
	got, _ := os.ReadFile(f)
	if string(got) != "old" {
		t.Errorf("restored content = %q, want old", got)
	}
	// ...and the previously-current "new" version is now itself in the trash.
	entries, _ := List()
	var foundNew bool
	for _, en := range entries {
		if b, _ := os.ReadFile(en.TrashPath); string(b) == "new" {
			foundNew = true
		}
	}
	if !foundNew {
		t.Error("the pre-existing file should have been backed up into the trash")
	}
}

func TestRestore_ConflictRename(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	f := filepath.Join(project, "doomed.txt")
	if err := os.WriteFile(f, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Move(f, project); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := find(t, f)
	res, err := Restore(e.ID, ConflictRename)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if res.RestoredTo == f || !strings.HasPrefix(res.RestoredTo, f+".restored-") {
		t.Fatalf("RestoredTo = %q, want a %s.restored-* sibling", res.RestoredTo, f)
	}
	if got, _ := os.ReadFile(f); string(got) != "new" {
		t.Errorf("original path should still hold the current file, got %q", got)
	}
	if got, _ := os.ReadFile(res.RestoredTo); string(got) != "old" {
		t.Errorf("renamed restore should hold the old content, got %q", got)
	}
}

func TestMove_Directory(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	dir := filepath.Join(project, "tree")
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Move(dir, project); err != nil {
		t.Fatalf("Move dir: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("Move must remove the original directory")
	}
	e := find(t, dir)
	if _, err := Restore(e.ID, ConflictAbort); err != nil {
		t.Fatalf("Restore dir: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "nested", "a.txt")); string(got) != "hi" {
		t.Errorf("restored dir content mismatch: %q", got)
	}
}

func TestDelete(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	f := filepath.Join(project, "doomed.txt")
	if err := os.WriteFile(f, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Move(f, project); err != nil {
		t.Fatal(err)
	}
	e := find(t, f)
	freed, err := Delete(e.ID)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if freed != 10 {
		t.Errorf("freed = %d, want 10", freed)
	}
	if _, err := os.Stat(e.TrashPath); !os.IsNotExist(err) {
		t.Error("trashed bytes should be gone after Delete")
	}
	if _, err := os.Stat(e.TrashPath + ".meta.json"); !os.IsNotExist(err) {
		t.Error("sidecar should be gone after Delete")
	}
}
