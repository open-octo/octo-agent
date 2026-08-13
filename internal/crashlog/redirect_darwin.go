package crashlog

import (
	"os"
	"syscall"
)

// redirectStderr makes fd 2 a second descriptor for f, so the runtime's writes
// to stderr land in the file. A .app launched from Finder inherits no usable
// stderr, which is the macOS version of the problem this package solves.
func redirectStderr(f *os.File) error {
	if err := syscall.Dup2(int(f.Fd()), int(os.Stderr.Fd())); err != nil {
		return err
	}
	// fd 2 is now its own descriptor for the same file, so f's is redundant —
	// and holding it open would leak it for the life of the process. The close
	// is best-effort: stderr is already redirected, and reporting a failure
	// here would tell the caller the opposite.
	_ = f.Close()
	return nil
}
