package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRenameWithRetry_WaitsForOpenDestination pins the Windows behaviour the
// retry exists for: with the destination held open by a reader (as an unlocked
// cachedRegistry read in another process does), a plain os.Rename fails with
// ERROR_ACCESS_DENIED, and renameWithRetry succeeds once the reader closes.
// Windows-only by filename — POSIX rename never fails this way.
func TestRenameWithRetry_WaitsForOpenDestination(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "session-groups.json")
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	reader, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: renaming over a file another handle has open is refused.
	if err := os.Rename(tmp, dst); err == nil {
		reader.Close()
		t.Skip("rename over an open file succeeded on this Windows/Go build; nothing to retry")
	}

	go func() {
		time.Sleep(150 * time.Millisecond)
		reader.Close()
	}()
	if err := renameWithRetry(tmp, dst); err != nil {
		t.Fatalf("renameWithRetry after reader closed: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("destination = %q, want the renamed content", got)
	}
}
