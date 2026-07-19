//go:build windows

package config

import (
	"syscall"
	"unsafe"
)

// withConfigLock is the Windows equivalent of flock — it uses LockFileEx
// to acquire an exclusive byte-range lock on the lockfile, runs fn, and
// releases the lock. The semantics match the Unix version: serialise
// concurrent Save calls across processes.
//
// Windows file locking is per-file-handle (not per-process like flock), so
// we keep the handle open for the duration of fn. Closing the handle
// (defer f.Close) releases the lock automatically; the explicit UnlockFileEx
// is belt-and-braces.
func withConfigLock(path string, fn func() error) error {
	pathUTF16, err := syscall.UTF16PtrFromString(lockFilePath(path))
	if err != nil {
		return fn()
	}
	fh, err := syscall.CreateFile(
		pathUTF16,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil,
		syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		// Can't open the lockfile — fall back to running without the lock.
		return fn()
	}
	defer syscall.CloseHandle(fh)

	// LockFileEx with LOCKFILE_EXCLUSIVE_LOCK locks byte range [0, 1)
	// exclusively. The large-integer struct encodes a 64-bit offset+length;
	// we lock the first byte only (enough to serialise — the lockfile is a
	// sidecar, its contents are never read or written).
	var overlapped syscall.Overlapped
	lockRange := [2]uint32{1, 0} // 1 byte at offset 0 (low DWORD)
	const lockfileExclusiveLock = 0x00000002
	const lockfileFailImmediately = 0x00000001
	lockFileExProc := syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
	r1, _, err := lockFileExProc.Call(
		uintptr(fh),
		uintptr(lockfileExclusiveLock|lockfileFailImmediately),
		0,
		uintptr(lockRange[0]), uintptr(lockRange[1]),
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if r1 == 0 {
		// Lock acquisition failed (another process holds it, or a transient
		// error) — fall back to running without the lock.
		return fn()
	}
	defer unlockFileEx(fh, &overlapped)
	return fn()
}

// unlockFileEx releases the byte-range lock acquired by LockFileEx. Best-effort
// — the handle close in withConfigLock's defer would release it anyway.
func unlockFileEx(fh syscall.Handle, overlapped *syscall.Overlapped) {
	unlockRange := [2]uint32{1, 0}
	unlockFileExProc := syscall.NewLazyDLL("kernel32.dll").NewProc("UnlockFileEx")
	unlockFileExProc.Call(
		uintptr(fh),
		0,
		uintptr(unlockRange[0]), uintptr(unlockRange[1]),
		uintptr(unsafe.Pointer(overlapped)),
	)
}

// lockFilePath returns the path of the lock sidecar file. Kept identical to
// the Unix version so the lockfile has the same name across platforms.
func lockFilePath(configPath string) string {
	return configPath + ".lock"
}
