package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"

	"github.com/open-octo/octo-agent/internal/executil"
	"github.com/open-octo/octo-agent/internal/server"
)

// serveWorkerEnv marks a process as the serve worker. The supervisor sets it
// on the child it spawns; setting it externally (systemd, launchd, a shell)
// skips the built-in supervisor so an external one can own the respawn via
// exit code server.ExitRestart.
const serveWorkerEnv = "OCTO_SERVE_WORKER"

// osExecutable is a stub hook for tests; production code calls os.Executable.
// spawnServeWorker caches its result at construction time (see its doc), and
// this variable is the seam that lets tests verify that caching.
var osExecutable = os.Executable

// superviseLoop drives the supervisor: spawn a worker, wait, and dispatch on
// its exit code — server.ExitRestart respawns (picking up a replaced binary),
// anything else propagates. A signal received while a worker runs is
// forwarded and permanently stops respawning: the user asked the whole serve
// to quit, and on POSIX the worker usually got the same terminal signal
// directly anyway.
//
// spawn starts one worker and returns a wait function (blocks until exit,
// returns the exit code) and a signal-forwarding function. Split out from
// the exec.Cmd mechanics so the loop is testable with fakes.
func superviseLoop(spawn func() (func() int, func(os.Signal), error), sigCh <-chan os.Signal, stderr io.Writer) int {
	quitting := false
	for {
		wait, forward, err := spawn()
		if err != nil {
			fmt.Fprintf(stderr, "octo serve: supervisor: %v\n", err)
			return 1
		}

		done := make(chan int, 1)
		go func() { done <- wait() }()

		var code int
	waitLoop:
		for {
			select {
			case sig := <-sigCh:
				quitting = true
				forward(sig)
			case code = <-done:
				break waitLoop
			}
		}

		// A signal racing the worker's exit may still be queued (select picks
		// randomly when both are ready); drain it so we don't respawn a
		// worker that is already doomed.
		select {
		case <-sigCh:
			quitting = true
		default:
		}

		if code == server.ExitRestart && !quitting {
			fmt.Fprintln(stderr, "octo serve: restarting worker")
			continue
		}
		return code
	}
}

// spawnServeWorker returns the production spawn function for superviseLoop:
// re-exec the current binary with `serve <args>` and the worker marker env.
//
// The binary path is resolved ONCE, at supervisor startup, and cached for the
// lifetime of the supervisor. This is load-bearing for the upgrade swap flow:
// swap() renames the running binary aside (octo → octo.old.<ts>.<pid>) and
// renames the new binary into place (.octo.new.<pid> → octo). On Linux,
// os.Executable() reads /proc/self/exe, which reflects the running inode's
// *current* name — so after the rename it returns the now-deleted aside name,
// and the next worker spawn fails with ENOENT. Caching the path at startup
// sidesteps this: the cached string still says "octo", and after the rename
// that path points at the new binary. (On macOS the same code path happens to
// work because KERN_PROC_PATHNAME is a static snapshot — but the cache makes
// the correctness explicit and platform-independent.)
func spawnServeWorker(args []string, stdout, stderr io.Writer) func() (func() int, func(os.Signal), error) {
	exe, resolveErr := osExecutable()
	return spawnServeWorkerWithResolver(args, stdout, stderr, func() (string, error) {
		return exe, resolveErr
	})
}

// spawnServeWorkerWithResolver is the testable core of spawnServeWorker.
// resolver is called on every worker spawn so tests can fake a binary path
// that changes between spawns without touching the real executable. The
// production resolver (a closure over a cached os.Executable() result)
// ignores this and always returns the same path, which is what makes the
// upgrade swap flow safe — see spawnServeWorker's doc comment.
func spawnServeWorkerWithResolver(args []string, stdout, stderr io.Writer, resolver func() (string, error)) func() (func() int, func(os.Signal), error) {
	return func() (func() int, func(os.Signal), error) {
		exe, err := resolver()
		if err != nil {
			return nil, nil, err
		}
		cmd := exec.Command(exe, append([]string{"serve"}, args...)...)
		// Windowless on Windows: the daemonized supervisor has no console, so
		// an unflagged console-subsystem worker would pop a visible window.
		executil.SetNoWindow(cmd)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Env = append(os.Environ(), serveWorkerEnv+"=1")
		if err := cmd.Start(); err != nil {
			return nil, nil, err
		}

		wait := func() int {
			err := cmd.Wait()
			if err == nil {
				return 0
			}
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				// Signal-killed workers report -1; normalise to a defined
				// crash code instead of leaking it through os.Exit (255).
				if c := ee.ExitCode(); c >= 0 {
					return c
				}
			}
			// Wait failed without a usable exit code (I/O error, killed by
			// signal, killed before start completed); treat as a crash.
			return 1
		}
		forward := func(sig os.Signal) {
			if cmd.Process == nil {
				return
			}
			// Windows: a console Ctrl-C event was already delivered to the
			// worker (same console group), so forwarding is redundant — and
			// the only mechanism available, Process.Kill, would land before
			// the worker's graceful Shutdown and orphan its background
			// processes. Let the worker handle the event it already has.
			if runtime.GOOS == "windows" {
				return
			}
			_ = cmd.Process.Signal(sig)
		}
		return wait, forward, nil
	}
}
