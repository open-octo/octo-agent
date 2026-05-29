//go:build windows

package tools

import (
	"os"
	"syscall"
)

// setProcessGroupOpts is a no-op on Windows.
func setProcessGroupOpts() *syscall.SysProcAttr { return nil }

// killProcessGroup kills the process on Windows.
// Windows doesn't have POSIX process groups in the same way, so we fall back
// to killing just the top-level process.
func killProcessGroup(p *os.Process) error {
	return p.Kill()
}
