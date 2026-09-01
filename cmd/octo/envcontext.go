package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/tools"
)

// buildEnvContext renders the session's "# Environment" block. The shared
// machine lines live in tools.BuildEnvContext; here the CLI contributes its
// cwd's git state (its cwd is a repository), which the server builder omits
// because its cwd is a workspace.
func buildEnvContext(cwd string) string {
	branch, dirty, ok := gitState(cwd)
	return tools.BuildEnvContext(cwd, branch, dirty, ok)
}

// gitState returns the current branch and whether the working tree has
// uncommitted changes. ok is false when cwd isn't a git repo or git is
// unavailable — callers should omit the git line in that case. A short
// timeout keeps a slow/hung git from stalling startup.
func gitState(cwd string) (branch string, dirty, ok bool) {
	if cwd == "" {
		return "", false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Verify cwd is actually inside a git repo, not just a subdir of one
	// reached by git's upward traversal.
	topLevel, err := gitRun(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false, false
	}
	// Normalize paths for comparison (git returns abs paths; cwd may be ".").
	// Use filepath.Clean to normalize separators so the comparison works on
	// Windows where git may return forward slashes.
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return "", false, false
	}
	absCwd = filepath.Clean(absCwd)
	// Resolve symlinks so worktrees on macOS (/tmp → /private/tmp) compare
	// correctly with git's --show-toplevel output.
	absCwd, _ = filepath.EvalSymlinks(absCwd)
	absTop := filepath.Clean(strings.TrimSpace(topLevel))
	if absTop == "" {
		return "", false, false
	}
	absTop, _ = filepath.EvalSymlinks(absTop)
	// cwd must be inside or equal to the repo root.
	if !strings.HasPrefix(absCwd, absTop) {
		return "", false, false
	}

	branchOut, err := gitRun(ctx, cwd, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", false, false
	}
	branch = strings.TrimSpace(branchOut)
	if branch == "" {
		return "", false, false
	}

	statusOut, err := gitRun(ctx, cwd, "status", "--porcelain")
	if err != nil {
		// We know the branch; report it as clean rather than dropping it.
		return branch, false, true
	}
	return branch, strings.TrimSpace(statusOut) != "", true
}

func gitRun(ctx context.Context, cwd string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	out, err := cmd.Output()
	return string(out), err
}
