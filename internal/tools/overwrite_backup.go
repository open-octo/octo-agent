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
//
// tool names the caller ("write_file" / "edit_file"), recorded as provenance.
// Returns the staged trash entry's id (empty when nothing was staged) so the
// tool can offer a one-click "undo" against it.
func backupBeforeOverwrite(ctx context.Context, abs, tool string) string {
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		return ""
	}
	if !overwriteBackupEnabled() {
		return ""
	}
	if gitTrackedClean(abs) {
		return ""
	}
	id, err := trash.Backup(abs, WorkingDirOrCWD(ctx), trash.Options{DeletedBy: tool, Kind: "overwrite"})
	if err != nil {
		return ""
	}
	return id
}

func overwriteBackupEnabled() bool {
	// LoadCached (not Load): this runs on every overwrite of an existing file,
	// so avoid re-parsing config.yml each time — matches how serve reads config.
	cfg, err := config.LoadCached()
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
