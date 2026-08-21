package server

import (
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/memory"
	"github.com/open-octo/octo-agent/internal/tools"
)

// adoptTaskWorkingDirs is the one-time reconciliation for sessions written
// before a working directory became a project's property rather than a
// session's. Such a session ran its tools in a directory it chose while its
// memory stayed in the shared tier — the split that removing the per-session
// control closes for new sessions. For the ones already on disk, the directory
// they picked is turned into what it should have been: a project.
//
// The workspace directory is the one thing never adopted. Every session that
// never chose a directory carries it instead (applyDefaultWorkspaceDir seeds
// it), so adopting those would file the whole task list under a single project
// named after the workspace — destroying the task/project distinction rather
// than honouring it. Both the built-in default (~/Octo) and whatever
// workspace_dir currently resolves to are excluded, and the built-in one
// unconditionally: a machine whose workspace_dir was changed later, or whose
// sessions were restored from a backup, still carries ~/Octo in sessions
// written before the change, and comparing only against the live setting would
// sweep exactly those into a project. Everything else is treated as a choice
// the user made and becomes a project.
//
// Idempotent, so it can run on every start: a session already in a project is
// skipped, and a second run over the same directory finds the project the first
// one made. Every failure is logged and skipped — this is a convenience pass,
// and a session that stays a task keeps working exactly as it did.
// underAnyDefault reports whether normalized dir lies under any of the
// "nobody chose this" roots — quiet skip, not a per-session validation error.
func underAnyDefault(defaults map[string]bool, dir string) bool {
	for d := range defaults {
		if strings.HasPrefix(dir, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (s *Server) adoptTaskWorkingDirs() {
	sessions, err := agent.ListSessions(0)
	if err != nil {
		slog.Warn("adopt task working dirs: list sessions", "err", err)
		return
	}

	// The directories that mean "nobody chose this": the workspace as configured
	// now, and the built-in default regardless of configuration (see above).
	defaults := map[string]bool{}
	addDefault := func(dir string) {
		if dir != "" {
			defaults[memory.NormalizeDir(dir)] = true
		}
	}
	addDefault(s.curWorkspaceDir())
	if builtin, berr := tools.ResolveWorkspaceDir(""); berr == nil {
		addDefault(builtin)
	}

	// Group the candidates by directory so one project is made per directory
	// rather than one per session.
	byDir := map[string][]string{}
	for _, sess := range sessions {
		if sess.WorkingDir == "" {
			continue
		}
		if projectForSession(sess.ID) != nil {
			continue // already where it belongs
		}
		dir := memory.NormalizeDir(sess.WorkingDir)
		if defaults[dir] {
			continue // a default, not a choice
		}
		if underAnyDefault(defaults, dir) {
			// Inside the workspace root — a task's throwaway workspace or some
			// other octo-owned ground. Never a choice, never a project.
			continue
		}
		byDir[sess.WorkingDir] = append(byDir[sess.WorkingDir], sess.ID)
	}
	if len(byDir) == 0 {
		return
	}

	for dir, ids := range byDir {
		if err := ensureProjectForDir(dir, ids...); err != nil {
			slog.Warn("adopt task working dirs: project for directory", "dir", dir, "err", err)
			continue
		}
		slog.Info("adopted sessions into a project for the directory they were working in",
			"dir", dir, "sessions", len(ids))
	}
}
