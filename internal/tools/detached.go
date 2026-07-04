package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// startDetached launches command as a daemon that outlives the agent harness:
// a new session (detachedCommand) immune to the harness's process-group kills,
// not bound to a cancelable context, with stdio pointed at a log file and
// /dev/null rather than the harness pipes. It is deliberately NOT registered in
// the BackgroundManager — fire-and-forget — so terminal_output / kill_shell and
// the on-exit KillAll all ignore it. The caller gets only the OS pid and the
// resolved log path; managing the process afterward is up to the user.
//
// logFile is resolved relative to the working dir when not absolute; an empty
// logFile defaults to "nohup.out" there, matching the nohup convention.
func startDetached(ctx context.Context, command, logFile string) (pid int, logPath string, err error) {
	workdir := WorkingDirOrCWD(ctx)

	logPath = resolveLogPath(workdir, logFile)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, "", fmt.Errorf("detached: open log %q: %w", logPath, err)
	}

	cmd, err := detachedCommand(ctx, command)
	if err != nil {
		_ = f.Close()
		return 0, "", fmt.Errorf("detached: build command: %w", err)
	}
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.Stdin = nil // nil Stdin = /dev/null; a detached daemon must not hold the harness's stdin

	if err := cmd.Start(); err != nil {
		_ = f.Close()
		return 0, "", fmt.Errorf("detached: start: %w", err)
	}
	pid = cmd.Process.Pid
	_ = f.Close() // the child holds its own dup of the fd

	// Reap in the background so the daemon doesn't linger as a zombie while octo
	// is still alive; once octo exits the child is reparented to init. We never
	// signal it — outliving the session is the whole point.
	go func() { _ = cmd.Wait() }()

	return pid, logPath, nil
}

// resolveLogPath picks the detached daemon's log file: an explicit path
// (~-expanded, made absolute against workdir if relative), else nohup.out in
// the working dir.
func resolveLogPath(workdir, logFile string) string {
	if logFile == "" {
		return filepath.Join(workdir, "nohup.out")
	}
	logFile = expandHome(logFile)
	if filepath.IsAbs(logFile) {
		return logFile
	}
	return filepath.Join(workdir, logFile)
}

// expandHome replaces a leading ~ or ~/ with the user's home directory.
func expandHome(path string) string {
	if path == "~" || (len(path) >= 2 && path[:2] == "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[1:])
		}
	}
	return path
}
