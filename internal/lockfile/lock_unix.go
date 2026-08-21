//go:build !windows

package lockfile

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// errLocked reports that someone else holds the lock — the one failure Acquire
// retries rather than giving up on.
var errLocked = errors.New("lockfile: held by another process")

// tryLockFD attempts an exclusive advisory lock without blocking. A contended
// lock comes back as EWOULDBLOCK (== EAGAIN on every platform Go supports),
// which is reported as errLocked so the caller can retry until its deadline.
func tryLockFD(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return errLocked
	}
	return err
}

// unlockFD releases it. Closing the descriptor would release it too; doing it
// explicitly keeps the release ordered before the close.
func unlockFD(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
