package server

import (
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/lockfile"
)

// TestRegistryLock_ReadsDoNotWaitOnTheFileLock pins the split between the two
// lock levels. Resolving a session's project happens once per session per
// listing, so if reads took the cross-process write lock, the server's listing
// endpoints would block on the CLI's writes (and flock grants no fairness, so
// a busy side can starve the other). Reads need nothing from it: cachedRegistry
// re-stats the file and re-reads it when another process has changed it.
//
// The lock is held here the way another process holds it — a separate open file
// description on the same lock file, which flock and LockFileEx both treat as a
// distinct holder — and the read must still complete.
func TestRegistryLock_ReadsDoNotWaitOnTheFileLock(t *testing.T) {
	isolatedHome(t)
	dir := t.TempDir()
	if err := EnsureProjectForDir(dir, "sess-1"); err != nil {
		t.Fatalf("seed a project: %v", err)
	}

	path, err := sessionGroupsPath()
	if err != nil {
		t.Fatalf("registry path: %v", err)
	}
	held := lockfile.Acquire(path)
	if held == nil {
		t.Skip("file locking unavailable here")
	}
	defer held.Release()

	done := make(chan string, 1)
	go func() {
		got := ""
		if p := projectForSession("sess-1"); p != nil {
			got = p.WorkingDir
		}
		done <- got
	}()

	select {
	case got := <-done:
		if got == "" {
			t.Error("read returned no project for a session that has one")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a registry READ blocked while another holder had the write lock — the read path must not take it")
	}
}

// TestRegistryLock_WriteWaitsForAnotherHolder is the same setup from the other
// side: a write MUST wait, which is the whole point of the file lock. Released
// from a timer so the write can eventually proceed and the test can assert it
// did, rather than only that it blocked.
func TestRegistryLock_WriteWaitsForAnotherHolder(t *testing.T) {
	isolatedHome(t)
	first := t.TempDir()
	if err := EnsureProjectForDir(first, "sess-1"); err != nil {
		t.Fatalf("seed a project: %v", err)
	}

	path, err := sessionGroupsPath()
	if err != nil {
		t.Fatalf("registry path: %v", err)
	}
	held := lockfile.Acquire(path)
	if held == nil {
		t.Skip("file locking unavailable here")
	}

	second := t.TempDir()
	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- EnsureProjectForDir(second, "sess-2") }()

	const holdFor = 250 * time.Millisecond
	select {
	case err := <-done:
		held.Release()
		t.Fatalf("write completed in %s without waiting for the lock holder (err=%v)", time.Since(started), err)
	case <-time.After(holdFor):
	}
	held.Release()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("write after the lock was released: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("write never completed after the lock was released")
	}
	if g := projectWithDir(t, second); g == nil {
		t.Errorf("the waiting write did not land a project for %s", second)
	}
}
