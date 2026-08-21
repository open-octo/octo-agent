package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/executil"
)

// All git subprocess calls behind the Git Diff panel live here
// (dev-docs/git-diff-panel-design.md). Every command is read-only — status,
// diff, rev-parse — because an agent session must never touch the user's git
// index. That rules out `git add -N` for untracked files; they are synthesised
// into new-file patches instead (synthUntrackedPatch).

const (
	// diffProbeTimeout bounds repository detection. Mounted folders on a dead
	// network share must not each burn the full data timeout.
	diffProbeTimeout = 2 * time.Second
	// diffDataTimeout bounds status and diff for one repository.
	diffDataTimeout = 15 * time.Second
	// untrackedMaxBytes caps how much of an untracked file is read into a
	// synthesised patch.
	untrackedMaxBytes = 1 << 20
	// binarySniffBytes is git's own heuristic window: a NUL byte in the first
	// 8 KB means binary.
	binarySniffBytes = 8 << 10
	// emptyTreeSHA is git's fixed empty-tree object, used as the diff base in a
	// repository with no commits so the merged worktree view still works.
	emptyTreeSHA = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
)

// errGitMissing reports that there is no git binary on this machine.
var errGitMissing = errors.New("git not available")

// gitAvailable reports whether git can be found at all, so the handler can
// answer once instead of every repository failing separately.
func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// runGit runs a read-only git command in dir and returns stdout.
func runGit(dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// core.quotepath=false keeps non-ASCII filenames verbatim UTF-8 instead of
	// git's \NNN octal escapes, which would otherwise reach the UI mangled.
	full := append([]string{"-C", dir, "-c", "core.quotepath=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	executil.SetNoWindow(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", errGitMissing
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("git %s timed out", args[0])
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], firstLine(msg))
	}
	return stdout.String(), nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// gitRepoRoot resolves dir to the root of the repository containing it, or
// reports false when dir is not inside one. Cheap stat gate first, same as
// folderGitBranch: no subprocess for the common non-repository case. A linked
// worktree carries .git as a file, which rev-parse handles natively.
func gitRepoRoot(dir string) (string, bool) {
	if dir == "" || !dirInsideGitRepo(dir) {
		return "", false
	}
	out, err := runGit(dir, diffProbeTimeout, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return "", false
	}
	return filepath.Clean(root), true
}

// diffRepoRootsForSession resolves the repositories a session may review: its
// own working directory plus its project's workspace and mounted source dirs.
// Callers never pass paths — that is the whole point (see handleGetSessionDiff)
// — so this is the only place the set is decided.
func (s *Server) diffRepoRootsForSession(sessionID string) []string {
	var dirs []string
	if cwd := s.sessionCwdByID(sessionID); cwd != "" {
		dirs = append(dirs, cwd)
	}
	if p := projectForSession(sessionID); p != nil {
		if p.WorkingDir != "" {
			dirs = append(dirs, p.WorkingDir)
		}
		dirs = append(dirs, p.SourceDirs...)
	}

	var roots []string
	seen := map[string]bool{}
	for _, dir := range dirs {
		root, ok := gitRepoRoot(dir)
		if !ok || seen[root] {
			continue
		}
		seen[root] = true
		roots = append(roots, root)
	}
	return roots
}

// statusEntry is one `git status --porcelain=v1 -z` record.
type statusEntry struct {
	x, y     byte
	path     string
	origPath string // renames and copies only
}

func (e statusEntry) untracked() bool { return e.x == '?' && e.y == '?' }

// staged reports whether the index side carries a change.
func (e statusEntry) staged() bool { return e.x != ' ' && e.x != '?' }

// status collapses the two porcelain columns into the single letter the panel
// shows. A worktree deletion wins over whatever the index says, because the
// net effect against HEAD is that the file is gone.
func (e statusEntry) status() string {
	switch {
	case e.untracked():
		return "?"
	case e.y == 'D':
		return "D"
	case e.x == 'U' || e.y == 'U':
		return "M" // unmerged renders as a modification
	case e.x != ' ':
		return string(e.x)
	default:
		return string(e.y)
	}
}

// gitStatus lists the repository's changed files.
//
// -z sidesteps git's filename quoting entirely, and -uall expands untracked
// directories into individual files — a bare `?? dir/` entry has no content to
// render.
func gitStatus(root string) ([]statusEntry, error) {
	out, err := runGit(root, diffDataTimeout, "status", "--porcelain=v1", "-z", "-uall")
	if err != nil {
		return nil, err
	}
	return parseStatusZ(out), nil
}

// parseStatusZ decodes the NUL-separated porcelain v1 stream. Rename and copy
// records span two fields, and in -z form the order is reversed relative to the
// human format: the new path comes first, then the original.
func parseStatusZ(out string) []statusEntry {
	fields := strings.Split(out, "\x00")
	var entries []statusEntry
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		e := statusEntry{x: f[0], y: f[1], path: f[3:]}
		if e.x == 'R' || e.x == 'C' || e.y == 'R' || e.y == 'C' {
			if i+1 < len(fields) {
				i++
				e.origPath = fields[i]
			}
		}
		entries = append(entries, e)
	}
	return entries
}

// gitDiffBase returns the revision to diff the worktree against: HEAD normally,
// the empty tree in a repository with no commits yet. Diffing against a tree
// (rather than falling back to --cached) keeps the merged staged+unstaged view
// intact in a fresh repository too.
func gitDiffBase(root string) string {
	if _, err := runGit(root, diffProbeTimeout, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		return emptyTreeSHA
	}
	return "HEAD"
}

// gitDiff runs the merged staged+unstaged diff, optionally scoped to one path.
func gitDiff(root string, paths ...string) (string, error) {
	args := []string{"diff", gitDiffBase(root), "--unified=3", "--find-renames", "--no-color"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	return runGit(root, diffDataTimeout, args...)
}

// gitHeadInfo reports the branch to show in the panel header, plus the short
// commit when HEAD is detached and the branch name is therefore useless.
func gitHeadInfo(root string) (branch, commit string) {
	if out, err := runGit(root, diffProbeTimeout, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		branch = strings.TrimSpace(out)
	}
	if branch == "" || branch == "HEAD" {
		// Detached, or a repository with no commits where rev-parse fails but
		// the branch ref already exists.
		if out, err := runGit(root, diffProbeTimeout, "branch", "--show-current"); err == nil {
			if cur := strings.TrimSpace(out); cur != "" {
				return cur, ""
			}
		}
	}
	if branch == "HEAD" {
		if out, err := runGit(root, diffProbeTimeout, "rev-parse", "--short", "HEAD"); err == nil {
			commit = strings.TrimSpace(out)
		}
	}
	return branch, commit
}

// resolveInRepo confines a repo-relative path to the repository root. The
// prefix check runs twice — once on the cleaned join and once after resolving
// symlinks — so neither `../` segments nor a symlink pointing outside the
// repository can escape (same model as resolveArtifactPath).
func resolveInRepo(root, rel string) (string, bool) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", false
	}
	root = filepath.Clean(root)
	abs := filepath.Clean(filepath.Join(root, rel))
	if !isInsideDir(root, abs) {
		return "", false
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// A dangling symlink or a file removed between status and read: not
		// something to serve, but not an escape either.
		return "", false
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	if !isInsideDir(realRoot, real) {
		return "", false
	}
	return abs, true
}

// isInsideDir reports whether path lies within dir. Both must be clean.
func isInsideDir(dir, path string) bool {
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}

// synthUntrackedPatch builds a new-file unified diff for an untracked file by
// reading it, rather than staging it with `git add -N`: the panel is read-only
// and must leave the user's index exactly as the agent left it.
//
// It reports binary for anything with a NUL byte in the first 8 KB, and
// truncated when the file is larger than untrackedMaxBytes.
func synthUntrackedPatch(root, rel string) (patch *diffPatch, binary, truncated bool, err error) {
	abs, ok := resolveInRepo(root, rel)
	if !ok {
		return nil, false, false, fmt.Errorf("path outside repository")
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		return nil, false, false, err
	}
	if !fi.Mode().IsRegular() {
		// Directories are expanded by -uall; symlinks and devices have no
		// reviewable content.
		return nil, false, false, fmt.Errorf("not a regular file")
	}

	f, err := os.Open(abs)
	if err != nil {
		return nil, false, false, err
	}
	defer f.Close()
	buf := make([]byte, untrackedMaxBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, false, false, err
	}
	content := buf[:n]

	sniff := content
	if len(sniff) > binarySniffBytes {
		sniff = sniff[:binarySniffBytes]
	}
	if bytes.IndexByte(sniff, 0) >= 0 {
		return nil, true, false, nil
	}
	truncated = fi.Size() > untrackedMaxBytes

	var lines []diffLine
	if len(content) > 0 {
		for _, l := range strings.Split(strings.TrimSuffix(string(content), "\n"), "\n") {
			lines = append(lines, diffLine{Kind: "add", Content: l})
		}
	}
	var hunks []diffHunk
	if len(lines) > 0 {
		hunks = []diffHunk{{
			Header: fmt.Sprintf("@@ -0,0 +1,%d @@", len(lines)),
			Lines:  lines,
		}}
	}
	return newDiffPatch("/dev/null", rel, hunks), false, truncated, nil
}
