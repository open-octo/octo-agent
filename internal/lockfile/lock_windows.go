package lockfile

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// errLocked reports that someone else holds the lock — the one failure Acquire
// retries rather than giving up on.
var errLocked = errors.New("lockfile: held by another handle")

// tryLockFD attempts an exclusive lock on the first byte without blocking.
// LOCKFILE_FAIL_IMMEDIATELY makes a contended lock return
// ERROR_LOCK_VIOLATION instead of waiting, which is reported as errLocked so
// the caller can retry until its deadline. Windows locks are per-handle rather
// than per-process, so the handle stays open for as long as the lock is held.
func tryLockFD(f *os.File) error {
	var ol windows.Overlapped
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errLocked
	}
	return err
}

// unlockFD releases it. The byte range must match the one locked.
func unlockFD(f *os.File) error {
	var ol windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
}
