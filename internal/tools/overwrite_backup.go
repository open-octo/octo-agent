package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/trash"
)

// backupBeforeOverwrite stages the current contents of abs into the trash
// before a tool overwrites it, so an overwrite that destroys uncommitted work
// is recoverable — the most common destructive action an agent takes, and the
// one the recycle bin most needs to guard.
//
// Best-effort: any failure is swallowed so it never blocks the write the model
// asked for (mirroring the safe-rm shell wrapper). It is skipped when:
//   - abs doesn't exist yet — a create, nothing to protect;
//   - the trash.overwrite_backup config toggle is off;
//   - abs is tracked by git and clean — `git checkout` already recovers the
//     exact bytes, so staging would only bloat the trash.
//
// The git check runs only for an *existing* file, so creating a brand-new file
// pays nothing.
func backupBeforeOverwrite(ctx context.Context, abs string) {
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		return
	}
	if !overwriteBackupEnabled() {
		return
	}
	if gitTrackedClean(abs) {
		return
	}
	_ = trash.Backup(abs, WorkingDirOrCWD(ctx))
}

func overwriteBackupEnabled() bool {
	cfg, err := config.Load()
	if err != nil {
		return true // default on
	}
	return cfg.OverwriteBackupEnabled()
}

// gitTrackedClean reports whether abs is a git-tracked file with neither
// unstaged nor staged changes — i.e. `git checkout` could recover its current
// bytes, making a trash backup redundant. Anything else (untracked, dirty, not
// a repo, git missing) returns false so the caller backs up.
func gitTrackedClean(abs string) bool {
	dir := filepath.Dir(abs)
	// Tracked at all?
	if err := runGitQuiet(dir, "ls-files", "--error-unmatch", "--", abs); err != nil {
		return false
	}
	// Unstaged changes? (git diff exits non-zero when the file differs.)
	if err := runGitQuiet(dir, "diff", "--quiet", "--", abs); err != nil {
		return false
	}
	// Staged changes?
	if err := runGitQuiet(dir, "diff", "--cached", "--quiet", "--", abs); err != nil {
		return false
	}
	return true
}

// runGitQuiet runs git in dir with output discarded, returning the run error
// (which carries the non-zero exit status git uses to signal its answer).
func runGitQuiet(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}
