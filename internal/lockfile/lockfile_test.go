package lockfile

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAcquire_ExcludesASecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")

	first := Acquire(path)
	if first == nil {
		t.Skip("file locking unavailable here")
	}

	// A second holder must not get in while the first has it. Short wait: the
	// point is that it gives up, not how long it is willing to wait.
	if second := acquire(path, 20*time.Millisecond); second != nil {
		second.Release()
		first.Release()
		t.Fatal("two holders got the same lock at once")
	}

	first.Release()
	if third := acquire(path, time.Second); third == nil {
		t.Error("lock was not available again after Release")
	} else {
		third.Release()
	}
}

// A holder that never lets go must not block a caller forever: the caller holds
// its own in-process lock across Acquire, so an indefinite wait would freeze
// this process on account of a stuck one somewhere else. Giving up unlocked
// risks a lost update, which is the lesser fault — the same trade the package
// makes when the lock file cannot be opened at all.
func TestAcquire_GivesUpAndProceedsUnlocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")

	held := Acquire(path)
	if held == nil {
		t.Skip("file locking unavailable here")
	}
	defer held.Release()

	const wait = 50 * time.Millisecond
	started := time.Now()
	got := acquire(path, wait)
	elapsed := time.Since(started)

	if got != nil {
		got.Release()
		t.Fatal("acquired a lock another holder still has")
	}
	if elapsed < wait {
		t.Errorf("gave up after %s, before the %s it was told to wait", elapsed, wait)
	}
	if elapsed > 2*time.Second {
		t.Errorf("waited %s despite a %s budget — the wait is not bounded", elapsed, wait)
	}
}

// Acquire returns nil rather than failing when the lock file cannot be created,
// and the caller is expected to carry on. Release must tolerate that nil.
func TestAcquire_UnwritableDirectoryProceedsUnlocked(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir", "registry.json")
	if h := Acquire(missing); h != nil {
		h.Release()
		t.Error("expected no handle for a path whose directory does not exist")
	}
}

// Release is a no-op on a nil handle (the "proceeded unlocked" case) and on a
// handle already released, so callers need no special casing in defers.
func TestRelease_NilAndRepeatedAreSafe(t *testing.T) {
	var nilHandle *Handle
	nilHandle.Release()

	path := filepath.Join(t.TempDir(), "registry.json")
	h := Acquire(path)
	if h == nil {
		t.Skip("file locking unavailable here")
	}
	h.Release()
	h.Release()
}
