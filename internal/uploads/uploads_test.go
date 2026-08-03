package uploads

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChannelTempDir(t *testing.T) {
	old := os.Getenv("TMPDIR")
	tmp := t.TempDir()
	os.Setenv("TMPDIR", tmp)
	defer os.Setenv("TMPDIR", old)

	dir, err := ChannelTempDir("telegram")
	if err != nil {
		t.Fatalf("ChannelTempDir: %v", err)
	}
	if filepath.Base(dir) != "octo-telegram" {
		t.Errorf("dir = %q, want basename octo-telegram", dir)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("ChannelTempDir did not create %q", dir)
	}
}

func TestSweep_RemovesOnlyOldFiles(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.txt")
	newPath := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(oldPath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldPath, old, old); err != nil {
		t.Fatal(err)
	}

	removed, freed, err := Sweep(dir, 24*time.Hour)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if removed != 1 || freed != 3 {
		t.Errorf("removed=%d freed=%d, want 1/3", removed, freed)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("old file should have been removed")
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Error("new file should still exist")
	}
}

func TestSweep_ZeroMaxAgeDisabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("x"), 0o600)
	old := time.Now().Add(-999 * 24 * time.Hour)
	os.Chtimes(path, old, old)

	removed, _, err := Sweep(dir, 0)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if removed != 0 {
		t.Error("maxAge <= 0 should disable the sweep")
	}
}

func TestSweep_MissingDirIsNotError(t *testing.T) {
	removed, freed, err := Sweep(filepath.Join(t.TempDir(), "does-not-exist"), time.Hour)
	if err != nil {
		t.Fatalf("Sweep on missing dir should not error: %v", err)
	}
	if removed != 0 || freed != 0 {
		t.Errorf("removed=%d freed=%d, want 0/0", removed, freed)
	}
}
