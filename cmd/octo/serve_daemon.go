package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// daemonPidFile returns the path of the daemon PID file, creating ~/.octo if needed.
func daemonPidFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".octo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "serve.pid"), nil
}

// daemonLogFile returns the path of the daemon log file, creating ~/.octo if needed.
func daemonLogFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".octo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "serve.log"), nil
}

// readPidFile reads an integer PID from path.
func readPidFile(path string) (int, error) {
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

// writePidFile writes pid to path.
func writePidFile(path string, pid int) error {
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", pid)), 0o644)
}

// startDaemon re-execs the current binary as a detached background process,
// writes its PID, and returns immediately. serveArgs must not contain -d/--daemon.
func startDaemon(serveArgs []string, stdout, stderr io.Writer) int {
	pidFile, err := daemonPidFile()
	if err != nil {
		fmt.Fprintf(stderr, "octo serve: daemon: %v\n", err)
		return 1
	}

	// Refuse to start a second daemon if one is already running.
	if pid, err := readPidFile(pidFile); err == nil && isProcessAlive(pid) {
		fmt.Fprintf(stderr, "octo serve: daemon already running (pid %d)\n", pid)
		return 1
	}
	_ = os.Remove(pidFile) // remove stale pid file if present

	logPath, err := daemonLogFile()
	if err != nil {
		fmt.Fprintf(stderr, "octo serve: daemon: %v\n", err)
		return 1
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(stderr, "octo serve: daemon: open log: %v\n", err)
		return 1
	}
	defer logFile.Close()

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "octo serve: daemon: %v\n", err)
		return 1
	}

	// The child runs as the supervisor (no --no-supervisor), logging to logFile.
	// We pass --no-supervisor so the child IS the top-level process we track via
	// the pid file — it owns its own worker subprocess internally via the
	// existing supervisor loop.  Passing the args as-is (minus -d) is correct:
	// the child's own supervisor will spawn a grandchild worker.
	cmd := exec.Command(exe, append([]string{"serve"}, serveArgs...)...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	setSysProcAttrDetach(cmd) // platform-specific: detach from terminal

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "octo serve: daemon: start: %v\n", err)
		return 1
	}

	pid := cmd.Process.Pid
	if err := writePidFile(pidFile, pid); err != nil {
		fmt.Fprintf(stderr, "octo serve: daemon: write pid: %v\n", err)
		_ = cmd.Process.Kill()
		return 1
	}

	fmt.Fprintf(stdout, "octo serve daemon started (pid %d)\n", pid)
	fmt.Fprintf(stdout, "logs: %s\n", logPath)
	return 0
}

// stopDaemon reads the PID file and terminates the daemon process.
func stopDaemon(stdout, stderr io.Writer) int {
	pidFile, err := daemonPidFile()
	if err != nil {
		fmt.Fprintf(stderr, "octo serve: %v\n", err)
		return 1
	}
	pid, err := readPidFile(pidFile)
	if err != nil {
		fmt.Fprintf(stderr, "octo serve: daemon not running\n")
		return 1
	}
	if !isProcessAlive(pid) {
		fmt.Fprintf(stderr, "octo serve: daemon not running (stale pid %d)\n", pid)
		_ = os.Remove(pidFile)
		return 1
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Fprintf(stderr, "octo serve: find process: %v\n", err)
		return 1
	}
	if err := terminateProcess(proc); err != nil {
		fmt.Fprintf(stderr, "octo serve: stop: %v\n", err)
		return 1
	}
	_ = os.Remove(pidFile)
	fmt.Fprintf(stdout, "octo serve daemon stopped (pid %d)\n", pid)
	return 0
}

// statusDaemon reports whether the daemon is running.
func statusDaemon(stdout, _ io.Writer) int {
	pidFile, err := daemonPidFile()
	if err != nil || func() bool { _, e := os.Stat(pidFile); return e != nil }() {
		fmt.Fprintln(stdout, "octo serve daemon: not running")
		return 0
	}
	pid, err := readPidFile(pidFile)
	if err != nil {
		fmt.Fprintln(stdout, "octo serve daemon: not running")
		return 0
	}
	if isProcessAlive(pid) {
		fmt.Fprintf(stdout, "octo serve daemon: running (pid %d)\n", pid)
	} else {
		fmt.Fprintf(stdout, "octo serve daemon: dead (stale pid %d)\n", pid)
		_ = os.Remove(pidFile)
	}
	return 0
}

// filterDaemonFlags strips -d / --daemon from the arg list before passing it
// to the detached child so the child doesn't try to daemonize again.
func filterDaemonFlags(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "-d" || a == "--daemon" || a == "-daemon" {
			continue
		}
		out = append(out, a)
	}
	return out
}
