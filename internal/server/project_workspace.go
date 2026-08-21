package server

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/tools"
)

// A project's workspace is an octo-owned directory under the configured
// workspace root — never a directory the user picked. That single form is what
// makes the rest of the lifecycle safe: deleting or sweeping a workspace can
// never touch user files, and the trust boundary stays "octo's ground plus
// explicitly mounted source folders".

// workspaceDirForProject returns (and creates) the workspace for a new project
// named name: <base>/<sanitized name>, with a numeric suffix when that
// directory is already taken — two projects may share a display name, but
// never a workspace. The directory is derived from the name once, at creation;
// renaming the project later does not move it (same rule cron projects follow).
func workspaceDirForProject(base, name string) (string, error) {
	if base == "" {
		return "", fmt.Errorf("project workspace: no workspace directory configured")
	}
	seg := dirNameFor(name)
	if seg == "" {
		return "", fmt.Errorf("project workspace: name %q yields no usable directory name", name)
	}
	base = expandDir(base)
	candidate := filepath.Join(base, seg)
	for i := 2; ; i++ {
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			break
		}
		if i > 99 {
			return "", fmt.Errorf("project workspace: too many name collisions for %q under %s", seg, base)
		}
		candidate = filepath.Join(base, fmt.Sprintf("%s-%d", seg, i))
	}
	if err := os.MkdirAll(candidate, 0o755); err != nil {
		return "", fmt.Errorf("project workspace: create %s: %w", candidate, err)
	}
	return candidate, nil
}

// resolveWorkspaceBase resolves the workspace root from configuration rather
// than from a running Server — project creation is also the CLI's path
// (EnsureProjectForDir), and it has no server to ask. Empty when the root
// cannot be resolved at all, which callers surface as a creation error.
func resolveWorkspaceBase() string {
	raw := ""
	if cfg, err := config.Load(); err == nil {
		raw = cfg.WorkspaceDir
	}
	if w, err := tools.ResolveWorkspaceDir(raw); err == nil {
		return w
	}
	return ""
}
