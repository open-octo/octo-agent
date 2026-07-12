package trash

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProvenance_RecordedAndListed: Move stamps DeletedBy/Kind into the sidecar
// and List surfaces them.
func TestProvenance_RecordedAndListed(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	f := filepath.Join(project, "notes.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Move(f, project, Options{DeletedBy: "session", Kind: "delete"}); err != nil {
		t.Fatal(err)
	}
	e := find(t, f)
	if e.DeletedBy != "session" || e.Kind != "delete" {
		t.Fatalf("provenance not recorded: deleted_by=%q kind=%q", e.DeletedBy, e.Kind)
	}
}

// TestBackup_ReturnsMatchingID: the id Backup returns addresses the same entry
// List reports (so a caller can offer undo against it).
func TestBackup_ReturnsMatchingID(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	f := filepath.Join(project, "doc.md")
	if err := os.WriteFile(f, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := Backup(f, project, Options{DeletedBy: "write_file", Kind: "overwrite"})
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("Backup returned an empty id")
	}
	entries, _ := List()
	var found *Entry
	for i := range entries {
		if entries[i].ID == id {
			found = &entries[i]
		}
	}
	if found == nil {
		t.Fatalf("no entry has the returned id %q", id)
	}
	if found.DeletedBy != "write_file" || found.Kind != "overwrite" {
		t.Errorf("provenance mismatch: %+v", found)
	}
}

// TestList_SkipsPhantomEntry: a sidecar whose bytes are gone (e.g. a restore
// that raced with eviction) is neither listed nor left on disk to reappear
// forever.
func TestList_SkipsPhantomEntry(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	f := filepath.Join(project, "notes.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Move(f, project); err != nil {
		t.Fatal(err)
	}
	e := find(t, f)
	// Simulate the race: bytes removed, sidecar left behind.
	os.Remove(e.TrashPath)

	entries, _ := List()
	if len(entries) != 0 {
		t.Fatalf("phantom entry should not be listed, got %d", len(entries))
	}
	if _, err := os.Stat(e.TrashPath + ".meta.json"); !os.IsNotExist(err) {
		t.Error("stale sidecar should have been cleaned up")
	}
}

// TestProvenance_MissingIsEmpty: an entry trashed without options (older
// sidecars) reads back with empty provenance, not an error.
func TestProvenance_MissingIsEmpty(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	f := filepath.Join(project, "plain.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Move(f, project); err != nil { // no Options
		t.Fatal(err)
	}
	e := find(t, f)
	if e.DeletedBy != "" || e.Kind != "" {
		t.Errorf("expected empty provenance, got deleted_by=%q kind=%q", e.DeletedBy, e.Kind)
	}
}
