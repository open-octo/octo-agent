package server

import (
	"sync"

	"github.com/open-octo/octo-agent/internal/lockfile"
)

// registryLock guards the session-groups registry. It has two levels, and
// which one a caller needs depends on whether it writes:
//
//   - Lock is the in-process mutex alone, for reads. It is what regCache and
//     every read path have always taken.
//   - LockWrite adds a cross-process file lock, for the read-modify-write
//     cycles. The CLI is a writer now (EnsureProjectForDir), so `octo` and a
//     running `octo serve` can reach for the registry at once; the atomic
//     temp-file + rename in saveRegistry keeps the file whole either way, but
//     two interleaved read-modify-write cycles would still lose one side's
//     update.
//
// Reads deliberately do NOT take the file lock. They are far more frequent —
// resolving one session's project happens per session per listing — and an
// exclusive lock on the read path would make the server's listing endpoints
// block on the CLI's writes and back, with no fairness guarantee between them.
// Nothing is lost by leaving it out: cachedRegistry re-stats the file and
// re-reads it when another process has changed it, which is what that cache is
// for. The one cost is on Windows, where a reader holding the file open makes
// a concurrent writer's rename fail transiently; saveRegistry retries through
// renameWithRetry to absorb that.
//
// That spares reads from waiting on another PROCESS directly, not from waiting
// on this one: a local writer holds mu across its file-lock wait, so reads queue
// behind it either way. What bounds that is lockfile.Timeout — the wait gives up
// and proceeds unlocked rather than parking on a holder that has stopped making
// progress, so a stuck process elsewhere cannot freeze this one's sidebar.
//
// Unlock covers both levels — it releases the file lock when one is held — so
// callers pair either level with a plain deferred Unlock.
type registryLock struct {
	mu sync.Mutex
	// held is the cross-process lock, non-nil only between LockWrite and
	// Unlock. Guarded by mu.
	held *lockfile.Handle
}

// Lock takes the in-process mutex, for a read. Same shape as sync.Mutex.Lock.
func (l *registryLock) Lock() {
	l.mu.Lock()
}

// LockWrite takes the in-process mutex and the cross-process file lock, for a
// read-modify-write cycle. Ordered mutex-then-file so the two levels can never
// deadlock against each other.
func (l *registryLock) LockWrite() {
	l.mu.Lock()
	path, err := sessionGroupsPath()
	if err != nil {
		// No resolvable path means no registry to write either; the write
		// itself will fail with the same error. Proceed with the in-process
		// lock rather than blocking here.
		return
	}
	l.held = lockfile.Acquire(path)
}

// Unlock releases the file lock (when held) before the mutex, so the next
// process in line only wins the file once this one has finished its write.
func (l *registryLock) Unlock() {
	l.held.Release()
	l.held = nil
	l.mu.Unlock()
}
