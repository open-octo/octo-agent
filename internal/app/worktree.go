package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/open-octo/octo-agent/internal/executil"
)

// worktree is a transient git worktree backing a sub-agent's isolation. The
// child's terminal + file tools are rooted in dir (via tools.WithWorkingDir),
// so its file mutations land here instead of the main checkout. On finish, a
// run that changed nothing is removed (worktree + branch); a run that produced
// changes commits them onto branch and is left for the caller to review/merge.
type worktree struct {
	dir    string // the linked worktree checkout
	branch string // the branch created for it
	repo   string // main repository root
}

// gitRun executes git in dir (empty ⇒ inherit cwd) and returns trimmed stdout,
// folding stderr into the error so failures are legible.
func gitRun(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	executil.SetNoWindow(cmd)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// newWorktree creates an isolated worktree + branch off the current HEAD. label
// (the sub-agent id) seeds a unique, human-readable branch/dir name. It errors
// when the cwd isn't inside a git repository.
func newWorktree(label string) (*worktree, error) {
	root, err := gitRun("", "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("worktree isolation requires a git repository: %w", err)
	}
	suffix := randHex()
	name := sanitizeRef(label)
	if name == "" {
		name = "agent"
	}
	branch := fmt.Sprintf("octo-wf/%s-%s", name, suffix)
	dir := filepath.Join(root, ".octo", "worktrees", name+"-"+suffix)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, fmt.Errorf("worktree dir: %w", err)
	}
	if _, err := gitRun(root, "worktree", "add", "-b", branch, dir); err != nil {
		return nil, err
	}
	return &worktree{dir: dir, branch: branch, repo: root}, nil
}

// finish reconciles the worktree per the "leave on a branch" contract: an
// unchanged run is cleaned up (worktree + branch removed) and returns ""; a run
// with changes commits them onto branch, leaves the worktree in place, and
// returns a note (branch, path, diff stat) to append to the agent's reply.
func (w *worktree) finish() string {
	status, err := gitRun(w.dir, "status", "--porcelain")
	if err != nil {
		return fmt.Sprintf("[worktree %s] could not read status: %v (left at %s)", w.branch, err, w.dir)
	}
	if status == "" {
		// No changes — auto-clean (worktree then branch). Best-effort.
		_, _ = gitRun(w.repo, "worktree", "remove", "--force", w.dir)
		_, _ = gitRun(w.repo, "branch", "-D", w.branch)
		return ""
	}
	if _, err := gitRun(w.dir, "add", "-A"); err != nil {
		return fmt.Sprintf("[worktree %s] changes left uncommitted at %s (git add failed: %v)", w.branch, w.dir, err)
	}
	if _, err := gitRun(w.dir, "commit", "--quiet", "-m", "octo worktree: "+w.branch); err != nil {
		return fmt.Sprintf("[worktree %s] changes left uncommitted at %s (git commit failed: %v)", w.branch, w.dir, err)
	}
	stat, _ := gitRun(w.dir, "show", "--stat", "--format=", "HEAD")
	return fmt.Sprintf("[worktree] changes committed on branch %q (checkout at %s). Review/merge it when ready:\n%s",
		w.branch, w.dir, strings.TrimSpace(stat))
}

// randHex returns 4 random bytes hex-encoded for unique branch/dir names.
func randHex() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0000"
	}
	return hex.EncodeToString(b[:])
}

// sanitizeRef reduces a label to a git-ref-safe slug.
func sanitizeRef(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '_' || r == '-' || r == '/':
			b.WriteByte('-')
		}
		if b.Len() >= 32 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}
