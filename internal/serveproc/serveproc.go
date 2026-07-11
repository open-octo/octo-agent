// Package serveproc holds the pid-file + process primitives that coordinate a
// single octo backend on a machine. Both the `octo serve -d` daemon (cmd/octo)
// and the desktop hub (cmd/octo-desktop) use it so they agree on one contract:
// the pid recorded in ~/.octo/serve.pid owns the port, and whoever finds a live
// pid there defers to it (or takes over). Keeping this in internal/ lets the
// nested desktop module share it without duplicating the platform-specific
// process checks.
package serveproc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// octoDir returns ~/.octo, creating it if needed.
func octoDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".octo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// PidPath returns the path of the backend pid file (~/.octo/serve.pid),
// creating ~/.octo if needed.
func PidPath() (string, error) {
	dir, err := octoDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "serve.pid"), nil
}

// LogPath returns the path of the daemon log file (~/.octo/serve.log),
// creating ~/.octo if needed.
func LogPath() (string, error) {
	dir, err := octoDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "serve.log"), nil
}

// ReadPid reads an integer pid from path.
func ReadPid(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var pid int
	if _, err := fmt.Sscan(strings.TrimSpace(string(data)), &pid); err != nil {
		return 0, fmt.Errorf("invalid pid file: %w", err)
	}
	return pid, nil
}

// WritePid writes pid to path.
func WritePid(path string, pid int) error {
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", pid)), 0o644)
}

// Running returns the pid of the live backend recorded in the pid file, if any.
// A missing pid file or a stale one (the recorded process is gone) yields
// (0, false); a stale file is removed as a side effect so the next starter has
// a clean slate.
func Running() (int, bool) {
	path, err := PidPath()
	if err != nil {
		return 0, false
	}
	pid, err := ReadPid(path)
	if err != nil {
		return 0, false
	}
	if !IsAlive(pid) {
		_ = os.Remove(path)
		return 0, false
	}
	return pid, true
}

// ReleaseOwned removes the pid file only if it still records pid — used by a
// hub on quit so it clears its own entry but never deletes one a successor
// (e.g. a process that took over the port) has since written.
func ReleaseOwned(pid int) {
	path, err := PidPath()
	if err != nil {
		return
	}
	if cur, err := ReadPid(path); err == nil && cur == pid {
		_ = os.Remove(path)
	}
}

// Stop terminates the backend recorded in the pid file and removes the file.
// It returns the pid it stopped, or (0, nil) if nothing live was recorded.
func Stop() (int, error) {
	path, err := PidPath()
	if err != nil {
		return 0, err
	}
	pid, err := ReadPid(path)
	if err != nil {
		return 0, nil // no pid file → nothing to stop
	}
	if !IsAlive(pid) {
		_ = os.Remove(path)
		return 0, nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, err
	}
	if err := Terminate(proc); err != nil {
		return 0, err
	}
	_ = os.Remove(path)
	return pid, nil
}
