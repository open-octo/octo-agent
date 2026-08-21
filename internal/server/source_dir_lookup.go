package server

import (
	"path/filepath"

	"github.com/open-octo/octo-agent/internal/memory"
)

// The CLI's window into the project registry. The CLI keeps its cwd wherever
// the user cd'ed — only session filing and memory scope follow the project —
// so what it needs are read-only claims lookups plus one explicit filing
// write, all exported here rather than scattered over cmd/octo.

// ProjectRef identifies a project to a caller outside this package.
type ProjectRef struct {
	ID           string
	Name         string
	WorkspaceDir string
}

// MemoryDir returns the project's ID-keyed memory directory — the same
// derivation Server.sessionMemDir uses, so notes written through the CLI land
// where every other surface reads them.
func (r ProjectRef) MemoryDir() (string, error) {
	return memory.DirForProjectID(r.ID, filepath.Base(r.WorkspaceDir))
}

// ProjectsClaimingDir returns every project that owns dir — as its workspace
// or as a mounted source folder. Order is registry order. More than one entry
// is a legitimate state (a source folder may be mounted by several projects);
// the interactive CLI resolves that with a picker, headless callers leave the
// session a task.
func ProjectsClaimingDir(dir string) []ProjectRef {
	if dir == "" {
		return nil
	}
	target := memory.NormalizeDir(dir)
	groupMu.Lock()
	defer groupMu.Unlock()
	gf, _, err := cachedRegistry()
	if err != nil {
		return nil
	}
	var out []ProjectRef
	for i := range gf.Groups {
		g := &gf.Groups[i]
		if g.WorkingDir == "" {
			continue
		}
		claimed := memory.NormalizeDir(g.WorkingDir) == target
		for _, sd := range g.SourceDirs {
			if claimed {
				break
			}
			claimed = memory.NormalizeDir(sd) == target
		}
		if claimed {
			out = append(out, ProjectRef{ID: g.ID, Name: g.Name, WorkspaceDir: g.WorkingDir})
		}
	}
	return out
}

// ProjectMemoryDirForSession returns the ID-keyed memory directory of the
// project owning sessionID, or "" when the session is in none — the CLI's
// resume path asks this so a project session opened from a terminal reads and
// writes the same notes it does everywhere else.
func ProjectMemoryDirForSession(sessionID string) string {
	p := projectForSession(sessionID)
	if p == nil {
		return ""
	}
	d, err := memory.DirForProjectID(p.ID, filepath.Base(p.WorkingDir))
	if err != nil {
		return ""
	}
	return d
}

// FileSessionInProject files sessionID under an already-chosen project — the
// write half of the CLI's multi-claim picker, where find-or-create semantics
// would pick the wrong project. One lock, one save.
func FileSessionInProject(projectID, sessionID string) error {
	groupMu.LockWrite()
	defer groupMu.Unlock()
	gf, err := loadRegistryFile()
	if err != nil {
		return err
	}
	if err := fileSessionInGroup(&gf, projectID, sessionID); err != nil {
		return err
	}
	return saveRegistry(gf)
}

// EnsureProjectForDirOnly finds or creates the project mounting dir and
// returns it WITHOUT filing any session — the CLI creates the project at
// startup so the very first session composes with the project's memory, and
// files itself only after its first save (a session that never says anything
// files nothing). The workspace defaults are never adopted, same as
// EnsureProjectForDir.
func EnsureProjectForDirOnly(dir string) (ProjectRef, error) {
	if dir == "" || isDefaultWorkspaceDir(dir) {
		return ProjectRef{}, nil
	}
	groupMu.LockWrite()
	defer groupMu.Unlock()
	gf, err := loadRegistryFile()
	if err != nil {
		return ProjectRef{}, err
	}
	gid, err := findOrCreateProject(&gf, dir)
	if err != nil {
		return ProjectRef{}, err
	}
	if err := saveRegistry(gf); err != nil {
		return ProjectRef{}, err
	}
	for i := range gf.Groups {
		if gf.Groups[i].ID == gid {
			return ProjectRef{ID: gid, Name: gf.Groups[i].Name, WorkspaceDir: gf.Groups[i].WorkingDir}, nil
		}
	}
	return ProjectRef{}, nil
}
