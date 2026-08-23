package server

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/memory"
)

// migrateProjectWorkspaces converts registries written before the workspace
// model — projects whose WorkingDir is a user directory — into the one shape
// every project has now: a generated workspace with the old directory mounted
// as a source folder. Three moves per project, in an order chosen so a crash
// between any two leaves nothing broken:
//
//  1. the project's memory directory moves from the old path-derived slug to
//     the ID-keyed one (rename; never onto existing content — losing notes is
//     the one non-negotiable here). The shared home tier is never moved, even
//     when a project pointed straight at $HOME: that slug IS the tier every
//     session reads. When several projects shared one directory, the first in
//     registry order takes the notes — the others start empty, which is the
//     the same place they would be after any other tie-break.
//  2. member sessions that never chose a directory (empty or seeded) get the
//     OLD project directory written back — with resolution now preferring a
//     session's own dir, this keeps every existing session running exactly
//     where it ran before the migration, byte for byte. Workspace semantics
//     apply only to sessions created after it.
//  3. the group itself: WorkingDir becomes a generated workspace and the old
//     directory becomes SourceDirs[0].
//
// Idempotent: a migrated project's WorkingDir lies under the workspace root
// and is skipped on the next start. Runs under one LockWrite with one save.
func (s *Server) migrateProjectWorkspaces() {
	base := s.curWorkspaceDir()
	if base == "" {
		return
	}
	normBase := memory.NormalizeDir(expandDir(base))

	groupMu.LockWrite()
	defer groupMu.Unlock()
	gf, err := loadRegistryFile()
	if err != nil {
		slog.Warn("workspace migration: load registry", "err", err)
		return
	}
	changed := false
	for i := range gf.Groups {
		g := &gf.Groups[i]
		if g.WorkingDir == "" {
			continue
		}
		old := g.WorkingDir
		normOld := memory.NormalizeDir(old)
		if normOld == normBase || strings.HasPrefix(normOld, normBase+string(filepath.Separator)) {
			continue // already workspace-form (new projects, cron)
		}

		// Registry-aware, not disk-based: a rerun after a crash between the
		// memory rename and the registry save must reuse the directory the
		// first run created, or the ID-keyed memory slug (which embeds the
		// workspace basename) would point away from the already-moved notes.
		workspace, werr := workspaceDirForMigration(gf.Groups, base, g.Name)
		if werr != nil {
			slog.Warn("workspace migration: generate workspace", "project", g.Name, "err", werr)
			continue
		}

		migrateProjectMemory(g.ID, old, workspace)
		writeBackMemberDirs(g.SessionIDs, old)

		g.WorkingDir = workspace
		if !dirInSet(old, g.SourceDirs) {
			g.SourceDirs = append([]string{old}, g.SourceDirs...)
		}
		changed = true
		slog.Info("migrated project to a generated workspace",
			"project", g.Name, "workspace", workspace, "mounted", old)
	}
	if !changed {
		return
	}
	if err := saveRegistry(gf); err != nil {
		slog.Warn("workspace migration: save registry", "err", err)
	}
}

// migrateProjectMemory renames the old path-slug memory directory to the
// ID-keyed one. Never onto existing content, never the shared home tier.
func migrateProjectMemory(projectID, oldDir, workspace string) {
	oldMem, err := memory.Dir(oldDir)
	if err != nil {
		return
	}
	if home, herr := memory.HomeDir(); herr == nil && home == oldMem {
		return // the shared tier is not a project directory to be moved
	}
	newMem, err := memory.DirForProjectID(projectID, filepath.Base(workspace))
	if err != nil {
		return
	}
	if _, err := os.Stat(oldMem); err != nil {
		return // nothing recorded there
	}
	if _, err := os.Stat(newMem); err == nil {
		return // never clobber what already exists
	}
	if err := os.Rename(oldMem, newMem); err != nil {
		slog.Warn("workspace migration: move memory", "from", oldMem, "to", newMem, "err", err)
	}
}

// writeBackMemberDirs stamps the old project directory onto member sessions
// that never chose one (empty or a seeded default) — the inverse of the old
// "project shadows the session" order, preserving where each session ran.
func writeBackMemberDirs(sessionIDs []string, oldDir string) {
	for _, id := range sessionIDs {
		sess, err := agent.LoadSession(id)
		if err != nil {
			continue // dead id in the registry; harmless by design
		}
		if sess.WorkingDir != "" && !isDefaultWorkspaceDir(sess.WorkingDir) {
			continue // the session chose this itself; nothing to preserve
		}
		if err := sess.SetWorkingDir(oldDir); err != nil {
			slog.Warn("workspace migration: write back session dir", "session", id, "err", err)
		}
	}
}
