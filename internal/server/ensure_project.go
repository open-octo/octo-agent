package server

import (
	"path/filepath"
	"strings"

	"github.com/open-octo/octo-agent/internal/memory"
	"github.com/open-octo/octo-agent/internal/tools"
)

// Turning a directory into a project is the one registry write with two
// callers in two processes: the startup reconciliation pass
// (adoptTaskWorkingDirs) and the CLI, which files a TUI session under a project
// for the directory it was started in. Both come through here so the
// find-or-create and the filing happen under one lock and one save.

// findOrCreateProject returns the id of the project working in dir, appending
// one named after the directory when none exists. In-memory: the caller holds
// groupMu.LockWrite and owns the save, which is what lets a create and a filing
// land as one write.
//
// The match is on the directory rather than the name, since names are not
// unique and the directory is what governs where sessions run. A directory that
// no longer validates (gone, unreadable) is an error rather than a group
// without one: a plain group would file sessions somewhere that answers none of
// the questions the directory did.
func findOrCreateProject(gf *groupFile, dir string) (string, error) {
	target := memory.NormalizeDir(dir)
	for i := range gf.Groups {
		if wd := gf.Groups[i].WorkingDir; wd != "" && memory.NormalizeDir(wd) == target {
			return gf.Groups[i].ID, nil
		}
	}

	validated, err := validateWorkingDir(dir)
	if err != nil {
		return "", err
	}
	name := filepath.Base(memory.NormalizeDir(validated))
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = validated
	}
	g := sessionGroup{ID: newGroupID(), Name: name, SessionIDs: []string{}, WorkingDir: validated}
	gf.Groups = append(gf.Groups, g)
	return g.ID, nil
}

// ensureProjectForDir files sessionIDs under the project working in dir,
// creating that project when none exists. One lock, one save: a second process
// cannot slip a duplicate project for the same directory in between the lookup
// and the create.
func ensureProjectForDir(dir string, sessionIDs ...string) error {
	groupMu.LockWrite()
	defer groupMu.Unlock()
	gf, err := loadRegistryFile()
	if err != nil {
		return err
	}
	gid, err := findOrCreateProject(&gf, dir)
	if err != nil {
		return err
	}
	// One session failing to file must not discard the project and the sessions
	// that did — save what landed and report the first failure.
	var firstErr error
	for _, id := range sessionIDs {
		if ferr := fileSessionInGroup(&gf, gid, id); ferr != nil && firstErr == nil {
			firstErr = ferr
		}
	}
	if serr := saveRegistry(gf); serr != nil {
		return serr
	}
	return firstErr
}

// EnsureProjectForDir files sessionID under the project working in dir,
// creating that project when none exists. Exported for the CLI: a TUI session
// records the directory it works in, and that directory is what a project is,
// so the session belongs in one from the start rather than only after the next
// `octo serve` runs its reconciliation pass. Being in a project is also what
// scopes the session's memory to the directory (Server.sessionMemDir) on every
// surface rather than just the CLI's own cwd-derived tier.
//
// Idempotent and cheap to call again: a session already in a project is left
// alone, and a directory that already has a project reuses it.
//
// No project is CREATED for the workspace directory — see
// isDefaultWorkspaceDir — but one the user made there is still joined: the rule
// is "accepting the default is not choosing a directory", not "the workspace can
// have no project". Skipping a project the user built by hand would leave
// terminal sessions out of a project that the web sessions in the same
// directory are in.
//
// Errors are returned for the caller to log and shrug off: a session that stays
// out of a project keeps working exactly as it did.
func EnsureProjectForDir(dir, sessionID string) error {
	dir, sessionID = strings.TrimSpace(dir), strings.TrimSpace(sessionID)
	if dir == "" || sessionID == "" {
		return nil
	}
	if projectForSession(sessionID) != nil {
		return nil // already where it belongs
	}
	if isDefaultWorkspaceDir(dir) && !ProjectExistsForDir(dir) {
		return nil
	}
	return ensureProjectForDir(dir, sessionID)
}

// isDefaultWorkspaceDir reports whether dir is the directory that means
// "nobody chose this": the workspace as configured now, or the built-in default
// regardless of configuration. Adopting those would file every session that
// merely accepted the default under a single project named after the workspace,
// destroying the task/project distinction rather than honouring it. The
// built-in one counts unconditionally because a machine whose workspace_dir was
// changed later still carries the old value in sessions written before the
// change.
//
// The same rule adoptTaskWorkingDirs applies, resolved from configuration
// rather than from a running Server — this is also the CLI's path, and it has
// no server to ask.
func isDefaultWorkspaceDir(dir string) bool {
	target := memory.NormalizeDir(dir)
	if target == "" {
		return false
	}
	// The configured workspace, and the built-in default regardless of
	// configuration (see above).
	configured := tools.ConfiguredWorkspaceDir()
	builtin, err := tools.ResolveWorkspaceDir("")
	if err != nil {
		builtin = ""
	}
	for _, candidate := range []string{configured, builtin} {
		if candidate != "" && memory.NormalizeDir(candidate) == target {
			return true
		}
	}
	return false
}
