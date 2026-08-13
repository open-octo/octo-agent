package crashlog

import (
	"os"
	"syscall"
)

// redirectStderr makes fd 2 a second descriptor for f. Dup3 rather than Dup2:
// Linux dropped the dup2 syscall on the newer architectures (arm64, riscv64),
// and syscall.Dup2 doesn't exist there.
func redirectStderr(f *os.File) error {
	if err := syscall.Dup3(int(f.Fd()), int(os.Stderr.Fd()), 0); err != nil {
		return err
	}
	// fd 2 is now its own descriptor for the same file, so f's is redundant —
	// and holding it open would leak it for the life of the process.
	return f.Close()
}
