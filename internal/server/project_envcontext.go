package server

import (
	"context"
	"fmt"
	"hash/fnv"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/memory"
)

// sourceDirsHash is the freeze-identity component for a project's mounted
// folders: the env context bakes the mount list and the output-dir marker
// into the prompt, so either changing must re-freeze the composed system on
// the session's next turn (Session.IsComposedFor's third dimension).
//
// The set is normalized and sorted before hashing — mount ORDER never changes
// the prompt text below, so letting it change the hash would churn the prompt
// cache for nothing. Empty for a task and for a zero-folder project, on
// purpose: both compose the same prompt a pre-workspace session did, and an
// empty hash matches the empty value stored on sessions written before the
// field existed.
func sourceDirsHash(proj *sessionGroup) string {
	if proj == nil || (len(proj.SourceDirs) == 0 && proj.OutputDir == "") {
		return ""
	}
	dirs := make([]string, len(proj.SourceDirs))
	for i, d := range proj.SourceDirs {
		dirs[i] = memory.NormalizeDir(d)
	}
	sort.Strings(dirs)
	h := fnv.New64a()
	for _, d := range dirs {
		_, _ = h.Write([]byte(d))
		_, _ = h.Write([]byte{0})
	}
	_, _ = h.Write([]byte("out:" + memory.NormalizeDir(proj.OutputDir)))
	return fmt.Sprintf("%016x", h.Sum64())
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
		b.WriteString("- Source folders this project works on (cd into them or address them by absolute path):\n")
		for _, d := range proj.SourceDirs {
			line := "  - " + d
			if branch, ok := folderGitBranch(d); ok {
				line += fmt.Sprintf(" (git branch: %s)", branch)
			}
			if proj.OutputDir != "" && memory.NormalizeDir(d) == memory.NormalizeDir(proj.OutputDir) {
				line += " [output folder]"
			}
			b.WriteString(line + "\n")
		}
	}
	if proj.OutputDir != "" {
		fmt.Fprintf(&b, "- Finished deliverables go to the output folder %s; keep drafts and intermediates in the working directory.\n", proj.OutputDir)
	}
	return b.String()
}

// folderGitBranch returns the current branch of the repository at dir, when
// dir is one. Best-effort with a short timeout — a slow or hung git must not
// stall composing a prompt.
func folderGitBranch(dir string) (string, bool) {
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
