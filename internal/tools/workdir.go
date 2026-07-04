package tools

import (
	"context"
	"os"
)

// workingDirKey carries an optional working directory for terminal commands.
// Callers stamp it (via WithWorkingDir) to redirect terminal commands to an
// isolated directory rather than the process CWD. Unset ⇒ commands run in the process
// CWD, the default everywhere else.
type workingDirCtxKey struct{}

// WithWorkingDir returns a context that roots terminal commands at dir. An
// empty dir is a no-op (returns ctx unchanged).
func WithWorkingDir(ctx context.Context, dir string) context.Context {
	if dir == "" {
		return ctx
	}
	return context.WithValue(ctx, workingDirCtxKey{}, dir)
}

// WorkingDir returns the working directory stamped into ctx, or "" if none.
func WorkingDir(ctx context.Context) string {
	if v, ok := ctx.Value(workingDirCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// WorkingDirOrCWD returns WorkingDir(ctx), falling back to the process CWD
// when ctx carries none. This is the "resolve where this turn's tools should
// operate" idiom every WorkingDir(ctx) consumer needs — before it was
// centralized here, startDetached, both branches of sandbox.go's
// shellCommand, and the workflow registry each reimplemented it inline.
// Errors from os.Getwd() are swallowed to "" (matching every prior inline
// copy of this fallback) — callers that can't resolve a directory at all are
// expected to handle an empty string themselves (e.g. by erroring or letting
// a downstream os/exec default apply).
func WorkingDirOrCWD(ctx context.Context) string {
	if dir := WorkingDir(ctx); dir != "" {
		return dir
	}
	cwd, _ := os.Getwd()
	return cwd
}
