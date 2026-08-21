package server

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/memory"
	"github.com/open-octo/octo-agent/internal/trash"
)

// A task — a session in no project — gets a throwaway workspace of its own
// under <workspace>/tasks/<sessionID>, instead of sharing the workspace root
// where two tasks writing the same filename overwrote each other. Session IDs
// are timestamp-prefixed, so the design's "timestamp directory" falls out for
// free along with an exact session↔directory mapping, which is what lets the
// delete path and the orphan sweep below reason about ownership safely.

// tasksRoot returns <workspace>/tasks, or "" when no workspace resolves.
func (s *Server) tasksRoot() string {
	ws := s.curWorkspaceDir()
	if ws == "" {
		return ""
	}
	return filepath.Join(ws, "tasks")
}

// underTasksRoot reports whether dir lies strictly under the tasks root — the
// only directories the trash/sweep paths may ever touch. Everything else,
// whatever a session's WorkingDir says, is presumed to be the user's.
func (s *Server) underTasksRoot(dir string) bool {
	root := s.tasksRoot()
	if root == "" || dir == "" {
		return false
	}
	return strings.HasPrefix(memory.NormalizeDir(dir), memory.NormalizeDir(root)+string(filepath.Separator))
}

// taskWorkspaceFor creates and returns the throwaway workspace for sessionID.
func (s *Server) taskWorkspaceFor(sessionID string) (string, error) {
	root := s.tasksRoot()
	if root == "" {
		return "", nil
	}
	dir := filepath.Join(root, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// stashTaskWorkspace moves a deleted session's throwaway workspace to the
// trash. Only a directory under the tasks root is handled — this must never
// grow a path that touches any other WorkingDir value.
func (s *Server) stashTaskWorkspace(workingDir string) {
	if !s.underTasksRoot(workingDir) {
		return
	}
	if _, err := os.Stat(workingDir); err != nil {
		return // never created, or already gone
	}
	if _, err := trash.Backup(workingDir, s.tasksRoot()); err != nil {
		slog.Warn("task workspace: trash", "dir", workingDir, "err", err)
		return // keep the directory rather than deleting without a copy
	}
	if err := os.RemoveAll(workingDir); err != nil {
		slog.Warn("task workspace: remove after trash", "dir", workingDir, "err", err)
	}
}

// sweepOrphanTaskWorkspaces trashes task workspaces whose session no longer
// exists on disk. Runs at startup with the other reconciliation passes;
// idempotent — a second run finds nothing to move.
func (s *Server) sweepOrphanTaskWorkspaces() {
	root := s.tasksRoot()
	if root == "" {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return // no tasks root yet — nothing to sweep
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := agent.LoadSession(e.Name()); err == nil {
			continue // its session is alive
		}
		s.stashTaskWorkspace(filepath.Join(root, e.Name()))
	}
}
