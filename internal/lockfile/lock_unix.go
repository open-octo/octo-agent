//go:build !windows

package lockfile

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// lockFD takes an exclusive advisory lock, waiting for any current holder. The
// wait is in the kernel, which queues waiters — a non-blocking poll would let
// one caller lose the race arbitrarily many times in a row.
//
// EINTR is retried rather than reported: Go's scheduler preempts with signals,
// so an interrupted flock says nothing about the lock.
func lockFD(f *os.File) error {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return err
	}
}

// unlockFD releases it. Closing the descriptor would release it too; doing it
// explicitly keeps the release ordered before the close.
func unlockFD(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
