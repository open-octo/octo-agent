// Package crashlog captures the output a Go process produces as it dies —
// panic traces, runtime fatal errors, anything written to stderr — into a file
// on disk.
//
// It exists for the desktop app. That binary is linked with `-H windowsgui` on
// Windows, so the process has no console and GetStdHandle(STD_ERROR_HANDLE)
// returns nothing to write to; on macOS a .app launched from Finder is no
// better off. The runtime writes a panic trace to file descriptor 2 directly,
// below the slog/log handlers the app installs, so an unrecovered panic in any
// goroutine takes the window down leaving no trace anywhere — the user sees the
// app vanish and has nothing to report. `octo serve -d` never had this problem:
// it hands the worker child a log file as its stderr, so the same panic lands
// in serve.log.
//
// Install closes that gap by pointing the OS-level stderr at a file, which is
// what makes it work for runtime output rather than only for Go code that
// happens to write through os.Stderr.
package crashlog

import (
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"github.com/open-octo/octo-agent/internal/logfile"
)

// Size bound for the crash log. Smaller than the serve.log bound on purpose:
// this file exists to be read (and pasted into a bug report), and a crash trace
// with every goroutine's stack is a few hundred KB at worst.
const (
	maxBytes = 1 << 20 // 1 MiB
	backups  = 2
)

// Install redirects this process's stderr — at the file-descriptor level, so
// the Go runtime's own writes follow — to path, appending a banner line that
// marks the start of this run. Crash traces carry no timestamp of their own, so
// without the banner a stack in the file can't be tied to the launch that
// produced it.
//
// It also raises the traceback level to "all", so a panic dumps every
// goroutine's stack rather than only the panicking one. Which goroutine died is
// rarely the whole story when the process is a hub running turns, tools and
// sub-agents concurrently. GOTRACEBACK still wins if the user set it higher —
// debug.SetTraceback ignores a level below the environment's.
//
// The redirection is permanent — stderr has to stay writable right up to the
// moment the process dies, which is the only moment this package cares about —
// and it replaces os.Stderr, so call it during startup, before anything else
// could be writing there. A failed Install leaves the process's original stderr
// untouched.
func Install(path, banner string) error {
	// Best-effort, like the daemon's open-time rotation: an oversized crash log
	// is still worth having, an absent one isn't.
	_ = logfile.RotateIfLarger(path, maxBytes, backups)
	// 0o600, not serve.log's 0o644: stderr is where several subsystems'
	// diagnostics converge (MCP server warnings and their child processes' own
	// stderr among them), so this file can end up holding more than stack
	// frames. internal/audit takes the same precaution for the same reason.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if f.Fd() == os.Stderr.Fd() {
		// Stderr was closed before we started (`2>&-`), so the open above
		// landed on its descriptor: the file already *is* stderr. Duplicating
		// it onto itself would be a no-op that then closes it (darwin) or a
		// plain EINVAL (linux), so adopt it instead — assigning os.Stderr also
		// keeps f reachable, which is what stops the descriptor being closed
		// out from under us.
		os.Stderr = f
	} else if err := redirectStderr(f); err != nil {
		// redirectStderr owns f from here on; it only reports failures that
		// left the original stderr in place.
		_ = f.Close()
		return err
	}
	debug.SetTraceback("all")
	fmt.Fprintf(os.Stderr, "\n=== %s | pid %d | %s ===\n", banner, os.Getpid(), time.Now().Format(time.RFC3339))
	return nil
}
