package tools

import "context"

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
