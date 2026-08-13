// Package panics turns a recovered panic into a logged error, for the detached
// goroutines that run a tool call's work.
//
// Those goroutines have no request scope above them to recover — no net/http
// handler, no agent turn — and the desktop app hosts the whole server in its
// own process. An unrecovered panic in one therefore doesn't just fail a tool
// call: it closes the user's window and takes every other session with it,
// historically without leaving so much as a stack trace behind (which is why
// ~/.octo/crash.log exists — see internal/crashlog).
//
// Recovering on its own is not enough. Most of these goroutines are the only
// thing that will ever mark their work finished — delivering a result the
// script blocks on, closing a channel a caller waits on, clearing a busy flag,
// recording an exit status — so a bare recover would trade a crash for a
// permanent hang: harder to diagnose, and just as fatal to the session. That
// half belongs to the caller: use the returned error to settle whatever the
// goroutine was responsible for.
package panics

import (
	"fmt"
	"log/slog"
	"runtime/debug"
)

// Error logs a recovered panic with its stack and returns it as an error,
// or nil when r is nil (no panic in flight).
//
// It takes the recovered value instead of calling recover() itself, because
// recover only works when called *directly* by a deferred function — a helper
// that hid the call inside itself would silently never recover anything:
//
//	defer func() {
//		if err := panics.Error(recover(), "what this goroutine does"); err != nil {
//			// settle whatever this goroutine owns
//		}
//	}()
func Error(r any, what string, attrs ...any) error {
	if r == nil {
		return nil
	}
	slog.Error("recovered panic in detached goroutine",
		append([]any{"what", what, "panic", r, "stack", string(debug.Stack())}, attrs...)...)
	return fmt.Errorf("%s panicked: %v", what, r)
}
