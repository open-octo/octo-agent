// Package lockfile provides an advisory cross-process lock for the files under
// ~/.octo that more than one octo process can write — the config and the
// session-groups registry.
//
// octo has several entry points against one ~/.octo — a TUI process, `octo
// serve`, the desktop shell, one-off subcommands — and the ones that write a
// shared file all do it as read-modify-write on the whole file. The atomic
// temp-file + rename those writers use keeps a file from ever being observed
// half-written, but it does not stop two interleaved read-modify-write cycles
// from losing one side's changes entirely: both read the same starting state
// and the second rename wins.
//
// The lock lives in a sibling file (`<path>.lock`) rather than on the data file
// itself, for two reasons. A writer that replaces the data file by rename
// changes its inode, so a lock held on the old one would guard nothing. And
// keeping it out of the data file keeps it out of the READ path — readers of
// these files never need the lock, and locking the data file would make every
// reader wait for every writer.
//
// The lock is advisory: only processes that call Acquire honour it. It is
// released by the kernel when the holder exits, so a crashed process leaves
// nothing stale behind — which is why this is a real flock rather than an
// O_EXCL lockfile with a staleness timeout. flock over NFS is unreliable; for
// the local-filesystem case a home directory normally is, it is sound.
package lockfile

import (
	"errors"
	"log/slog"
	"os"
	"time"
)

// Handle is a held lock. Release it when the write is done.
type Handle struct {
	f *os.File
}

// Timeout bounds how long Acquire waits for a holder to let go. Writes under
// this lock are milliseconds (marshal, temp write, rename), so a wait longer
// than this means the holder is not making progress — stopped, or stuck on a
// network filesystem — rather than merely busy.
const Timeout = 5 * time.Second

// pollInterval is how often a contended lock is retried. Short enough that the
// common case (a holder mid-write) is indistinguishable from waiting on the
// kernel, long enough not to spin.
const pollInterval = 2 * time.Millisecond

// Acquire takes an exclusive lock for path, creating the sibling lock file if
// needed, and waits up to Timeout for it.
//
// Returns nil when the lock could not be taken — an unwritable directory, a
// filesystem without locking, or a holder that never let go. Callers treat nil
// as "proceed anyway": refusing the write because the lock is unavailable is
// worse than the rare lost update the lock exists to prevent. Release is safe
// on a nil Handle, so callers need no special case.
//
// The wait is bounded rather than indefinite because callers hold their own
// in-process lock across this call. A blocking acquire would let one stuck
// process anywhere on the machine freeze every reader in this one, turning a
// mechanism meant to prevent a lost update into an outage — the opposite of the
// trade this package makes everywhere else.
func Acquire(path string) *Handle {
	return acquire(path, Timeout)
}

// acquire is Acquire with the wait made explicit, so the give-up path can be
// tested without a test that waits Timeout.
func acquire(path string, timeout time.Duration) *Handle {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		slog.Debug("lockfile: open failed, proceeding unlocked", "path", path, "err", err)
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		err = tryLockFD(f)
		if err == nil {
			return &Handle{f: f}
		}
		if !errors.Is(err, errLocked) {
			slog.Debug("lockfile: lock failed, proceeding unlocked", "path", path, "err", err)
			f.Close()
			return nil
		}
		if time.Now().After(deadline) {
			slog.Warn("lockfile: still held after waiting, proceeding unlocked — a concurrent write may be lost",
				"path", path, "waited", timeout)
			f.Close()
			return nil
		}
		time.Sleep(pollInterval)
	}
}

// Release unlocks and closes the handle. A no-op on nil.
func (h *Handle) Release() {
	if h == nil || h.f == nil {
		return
	}
	if err := unlockFD(h.f); err != nil {
		slog.Debug("lockfile: unlock failed", "err", err)
	}
	h.f.Close()
	h.f = nil
}
