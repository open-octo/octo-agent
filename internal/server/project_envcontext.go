package server

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/hooks"
	"github.com/open-octo/octo-agent/internal/memory"
)

// sourceDirsHash is the freeze-identity component for a project's mounted
// folders: the env context bakes the mount list and the output-dir marker
// into the prompt, so either changing must re-freeze the composed system on
// the session's next turn (Session.IsComposedFor's third dimension).
//
// The set hashes in MOUNT order, which is also the order the env context
// lists folders and the order their .octorules layer into the prompt — so any
// reordering that rewords the frozen prompt re-freezes it, and nothing else
// does.
//
// Empty ONLY for a task: being in a project at all changes the prompt (the
// Project block below exists), so a zero-folder project still hashes to a
// non-empty identity. The empty value therefore keeps matching what task
// sessions and pre-workspace sessions have stored, while a project session
// written before this feature re-freezes once and picks the block up — the
// same one-time cost the cwd dimension had when it joined the identity.
func sourceDirsHash(proj *sessionGroup) string {
	if proj == nil {
		return ""
	}
	dirs := make([]string, len(proj.SourceDirs))
	for i, d := range proj.SourceDirs {
		dirs[i] = memory.NormalizeDir(d)
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte("project\x00"))
	for _, d := range dirs {
		_, _ = h.Write([]byte(d))
		_, _ = h.Write([]byte{0})
	}
	_, _ = h.Write([]byte("out:" + memory.NormalizeDir(proj.OutputDir)))
	return fmt.Sprintf("%016x", h.Sum64())
}

// projSourceDirs is the nil-safe accessor the compose and hook-engine sites
// use — a task (nil project) contributes no mounts anywhere.
func projSourceDirs(p *sessionGroup) []string {
	if p == nil {
		return nil
	}
	return p.SourceDirs
}

// sourceRuleDirs are the mounted folders whose .octorules layer into the
// composed prompt: every mount except the one that IS the cwd — a migrated
// session runs in its old project directory, which is also SourceDirs[0], and
// without this its conventions would appear twice in the frozen prompt.
func sourceRuleDirs(cwd string, p *sessionGroup) []string {
	dirs := projSourceDirs(p)
	if len(dirs) == 0 {
		return nil
	}
	normCwd := memory.NormalizeDir(cwd)
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if memory.NormalizeDir(d) == normCwd {
			continue
		}
		out = append(out, d)
	}
	return out
}

// sourceHookDirs are the mounted folders whose .octo/hooks.yml the engine
// loads: cwd-equal mounts are skipped (same double-load as above), and each
// remaining folder must pass the SAME fingerprint trust gate a working_dir
// retarget does. "Mounting is the trust grant" holds only for a mount a
// person made — the UI write paths record the grant (trustMountedHooks); a
// mount the migration manufactured records nothing, so hooks the old trust
// gate refused to load do not silently start executing after an upgrade.
func sourceHookDirs(cwd string, p *sessionGroup) []string {
	dirs := sourceRuleDirs(cwd, p)
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if hooksTrustedAt(d) {
			out = append(out, d)
		}
	}
	return out
}

// hooksTrustedAt reports whether dir's project hooks file is fingerprint-
// trusted — the standalone form of Server.projectHooksTrusted, shared with the
// env-context markers so "loads hooks" is only ever claimed for a file that
// actually will.
func hooksTrustedAt(dir string) bool {
	path := hooks.ProjectConfigPath(dir)
	if path == "" {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return hooks.IsTrusted(path, hooks.Fingerprint(b))
}

// trustMountedHooks records the trust grant a person just made by mounting
// dirs into a project: each folder's hooks file (when present) is trusted at
// its current fingerprint, exactly as a CLI approval would record it. Content
// that changes later must be re-granted, same as everywhere else.
func trustMountedHooks(dirs []string) {
	for _, d := range dirs {
		path := hooks.ProjectConfigPath(d)
		if path == "" {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if err := hooks.RecordTrust(path, hooks.Fingerprint(b)); err != nil {
			slog.Warn("mount trust grant", "dir", d, "err", err)
		}
	}
}

// appendProjectEnvContext extends a turn's env context for a project session:
// the working directory is declared to be the project's own scratch/output
// ground, and each mounted source folder is listed with its git branch (when
// it is a repository) so the model knows where the real work lives and cd's
// there itself. nil proj (a task) returns envCtx untouched, byte for byte —
// a task's prompt must not change because this feature exists.
//
// Like the rest of the env context this is rendered once per freeze, not per
// turn: branches drift, and that is the same accepted staleness the CLI's git
// line has always had.
func appendProjectEnvContext(envCtx string, proj *sessionGroup) string {
	if proj == nil {
		return envCtx
	}
	var b strings.Builder
	b.WriteString(envCtx)
	b.WriteString("\n## Project\n\n")
	b.WriteString("- The working directory is this project's own workspace: use it for scratch work and anything not asked to land elsewhere. It is octo's ground — no repository lives at the cwd itself.\n")
	if len(proj.SourceDirs) > 0 {
		// Mount order — the order the hooks/rules load in, and the order the
		// hash covers, so the listing, the loading, and the freeze identity
		// can never disagree.
		dirs := proj.SourceDirs
		b.WriteString("- Source folders this project works on (cd into them or address them by absolute path):\n")
		loadsAnything := false
		for _, d := range dirs {
			line := "  - " + d
			if branch, ok := folderGitBranch(d); ok {
				line += fmt.Sprintf(" (git branch: %s)", branch)
			}
			if proj.OutputDir != "" && memory.NormalizeDir(d) == memory.NormalizeDir(proj.OutputDir) {
				line += " [output folder]"
			}
			// Mounted folders' conventions and hooks load into this session
			// (mounting was the trust grant) — say WHERE behaviour comes
			// from, or a multi-repo pile of hooks becomes untraceable.
			if _, err := os.Stat(filepath.Join(d, ".octorules")); err == nil {
				line += " [loads .octorules]"
				loadsAnything = true
			}
			if hooksTrustedAt(d) {
				line += " [loads hooks]"
				loadsAnything = true
			}
			b.WriteString(line + "\n")
		}
		if loadsAnything {
			b.WriteString("- Folders marked [loads …] contribute their conventions/hooks to this session, in the order listed.\n")
		}
	}
	if proj.OutputDir != "" {
		fmt.Fprintf(&b, "- Finished deliverables go to the output folder %s; keep drafts and intermediates in the working directory.\n", proj.OutputDir)
	}
	return b.String()
}

// dirInsideGitRepo reports whether dir or any ancestor holds a .git entry — a
// directory OR a file, since linked worktrees carry a .git file. Pure stat
// walk, no subprocess: callers run it on hot paths (prompt composition, every
// CLI start, the adoption pass).
func dirInsideGitRepo(dir string) bool {
	if dir == "" {
		return false
	}
	dir = filepath.Clean(dir)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// DirInsideGitRepo is dirInsideGitRepo for the CLI, whose three-state
// adoption applies the same "is this directory worth a project row" gate the
// server's adoption pass does.
func DirInsideGitRepo(dir string) bool { return dirInsideGitRepo(dir) }

// folderGitBranch returns the current branch of the repository at dir, when
// dir is one. Best-effort with a short timeout — a slow or hung git must not
// stall composing a prompt.
func folderGitBranch(dir string) (string, bool) {
	// Cheap stat gate before spawning git: N mounted folders on a dead
	// network mount must not each burn the full subprocess timeout while a
	// prompt is being composed. A .git anywhere up the tree (file or dir —
	// linked worktrees carry a file) is the go-ahead.
	if !dirInsideGitRepo(dir) {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	branch := strings.TrimSpace(string(out))
	if err != nil || branch == "" {
		return "", false
	}
	return branch, true
}
