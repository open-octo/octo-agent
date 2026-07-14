//go:build !windows

package executil

import "os/exec"

func SetNoWindow(cmd *exec.Cmd) {}
