package server

import (
	"log/slog"
	"strings"
	"unicode"
)

// cronProjectDir is the directory a scheduled task's runs work in:
// <workspace>/<task name>, created on demand.
//
// Every scheduled task gets one, which is what makes its run cluster a project
// rather than a name with sessions under it. Before this, a run had no working
// directory at all — the scheduler does not seed one the way the HTTP create
// path does — so it fell through to the server's own launch directory, and every
// scheduled task on the machine ran its tools in whatever directory `octo serve`
// happened to be started from. A task that writes a file wrote it there; two
// tasks writing the same filename overwrote each other.
//
// One directory per task also gives each task its own memory tier (memory is
// scoped by project), so a daily report remembers its own history instead of
// sharing the machine-wide tier with everything else.
//
// The directory is derived from the name once, at creation, and then belongs to
// the project: renaming the task renames the row, not the directory. A rename
// that moved the directory would either strand everything the task has written
// there or have to move it, and neither is what someone typing a better title
// asked for.
func (s *Server) cronProjectDir(taskName, taskID string) string {
	base := s.curWorkspaceDir()
	if base == "" {
		return ""
	}
	// Project workspaces now share this namespace and naming rule, so the
	// candidate steps aside when another group already claims it — a project
	// named like the task must not have a cron task quietly move into its
	// workspace. The task's own directory is reused across repairs as before.
	groupMu.Lock()
	groups, err := loadSessionGroups()
	groupMu.Unlock()
	if err != nil {
		slog.Warn("cron project dir: load registry", "task", taskName, "err", err)
		groups = nil
	}
	dir, err := workspaceDirForTask(groups, base, taskName, taskID)
	if err != nil {
		slog.Warn("cron project dir: create", "task", taskName, "err", err)
		return ""
	}
	return dir
}

// dirNameFor turns a task name into one path segment. Non-ASCII is kept — a
// Chinese or Japanese task name makes a perfectly good directory name — and only
// what a path cannot carry is replaced: separators, the characters Windows
// reserves, and control characters. Leading dots go too, so a task named ".ssh"
// cannot produce a hidden directory.
func dirNameFor(taskName string) string {
	var b strings.Builder
	for _, r := range taskName {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			b.WriteRune('-')
		case unicode.IsControl(r):
			// dropped
		default:
			b.WriteRune(r)
		}
	}
	name := strings.TrimSpace(b.String())
	name = strings.Trim(name, ".-")
	// Windows also refuses a trailing space or dot on any path component.
	name = strings.TrimRight(name, " .")
	if len(name) > 64 {
		name = strings.TrimSpace(name[:64])
	}
	return name
}
