package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/serveproc"
)

// daemonReadyTimeout bounds how long startDaemon waits for the spawned server
// to begin accepting connections before it returns anyway.
const daemonReadyTimeout = 15 * time.Second

// The pid-file, log-file, and process primitives live in internal/serveproc so
// the desktop hub (cmd/octo-desktop) shares the exact same coordination. These
// thin aliases keep the daemon code below reading as before.
var (
	daemonPidFile    = serveproc.PidPath
	daemonLogFile    = serveproc.LogPath
	readPidFile      = serveproc.ReadPid
	writePidFile     = serveproc.WritePid
	isProcessAlive   = serveproc.IsAlive
	terminateProcess = serveproc.Terminate
)

// startDaemon re-execs the current binary as a detached background process,
// writes its PID, and waits (up to daemonReadyTimeout) for it to start
// accepting connections so callers — the Windows installer's post-install
// launch, a shell one-liner — can open the dashboard the moment this returns.
// serveArgs must not contain -d/--daemon.
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

	// Wait for the server to bind so a caller that opens the dashboard right
	// after this returns doesn't hit a not-yet-listening port. A bind failure
	// (e.g. port in use) shows up as the child exiting during the wait.
	addr := daemonDialAddr(serveArgs)
	if waitDaemonReady(addr, pid, daemonReadyTimeout) {
		fmt.Fprintf(stdout, "octo serve daemon started (pid %d), ready at http://%s\n", pid, addr)
	} else if !isProcessAlive(pid) {
		fmt.Fprintf(stderr, "octo serve: daemon exited during startup; see %s\n", logPath)
		_ = os.Remove(pidFile)
		return 1
	} else {
		fmt.Fprintf(stdout, "octo serve daemon started (pid %d); not yet accepting connections — check %s\n", pid, logPath)
	}
	fmt.Fprintf(stdout, "logs: %s\n", logPath)
	return 0
}

// daemonDialAddr derives a loopback-dialable host:port from the serve --addr
// flag (default 127.0.0.1:8088). A wildcard or empty host is dialed on
// 127.0.0.1 — the server always listens there too.
func daemonDialAddr(serveArgs []string) string {
	addr := "127.0.0.1:8088"
	for i, a := range serveArgs {
		switch {
		case a == "-addr" || a == "--addr":
			if i+1 < len(serveArgs) {
				addr = serveArgs[i+1]
			}
		case strings.HasPrefix(a, "-addr="):
			addr = strings.TrimPrefix(a, "-addr=")
		case strings.HasPrefix(a, "--addr="):
			addr = strings.TrimPrefix(a, "--addr=")
		}
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// waitDaemonReady blocks until the daemon accepts a TCP connection on addr, the
// child process dies, or timeout elapses. Returns true once a connection
// succeeds.
func waitDaemonReady(addr string, pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
		if !isProcessAlive(pid) {
			return false
		}
		time.Sleep(150 * time.Millisecond)
	}
	return false
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
