//go:build windows

package executil

import (
	"os/exec"
	"syscall"
)

// CREATE_NO_WINDOW prevents a console subsystem child from creating a visible
// console window when spawned from a GUI process.
const createNoWindow = 0x08000000

func SetNoWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}
