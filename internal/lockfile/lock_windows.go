package lockfile

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFD takes an exclusive lock on the first byte, waiting for any current
// holder. Without LOCKFILE_FAIL_IMMEDIATELY the wait happens in the kernel,
// which queues waiters — polling with the immediate-fail flag instead let one
// caller lose the race arbitrarily many times in a row, and a caller that
// eventually gives up writes unlocked.
//
// Windows locks are per-handle rather than per-process, so the handle stays
// open for as long as the lock is held.
func lockFD(f *os.File) error {
	var ol windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &ol)
}

// unlockFD releases it. The byte range must match the one locked.
func unlockFD(f *os.File) error {
	var ol windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
}
