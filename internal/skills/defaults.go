package skills

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// defaultsFS holds the skills shipped with the binary. They are the curated
// set every install gets out of the box; MaterializeDefaults writes them to
// disk so they're discoverable, listable, and overridable like any other
// skill. Embedding (rather than downloading) keeps a fresh install offline-
// capable and version-locked to the binary — no network, no supply chain.
//
// The `all:` prefix is deliberate: plain directory embedding skips files
// whose names start with "." or "_", which silently drops skill files that
// need to ship — e.g. ppt-master's Python package markers (`__init__.py`),
// its underscore-prefixed modules (`_dispatcher.py`, `_batch.py`, …), and
// the `_index.md` catalogs that several skills' instructions read.
//
//go:embed all:defaults
var defaultsFS embed.FS

// defaultStampFile records which binary version last materialized the default
// skills, so MaterializeDefaults can no-op until the version changes.
const defaultStampFile = ".octo-version"

// defaultSkillsRoot returns ~/.octo/skills-default — a dedicated, octo-managed
// directory kept separate from ~/.octo/skills so refreshing the defaults never
// touches a user's own skills. A var so tests can redirect it.
var defaultSkillsRoot = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".octo", "skills-default")
}

// DefaultRoot is the on-disk location of the materialized default skills
// (~/.octo/skills-default), exported for `octo skills path`.
func DefaultRoot() string { return defaultSkillsRoot() }

// UserRoot is the user-level skills directory (~/.octo/skills), exported for
// `octo skills path`.
func UserRoot() string { return userSkillsRoot() }

// MaterializeDefaults writes the embedded default skills to the default root
// when the on-disk version stamp doesn't match version. It's a fast no-op once
// the install is current (a single stamp read). Best-effort: the caller should
// ignore the error so a read-only HOME never blocks a session.
func MaterializeDefaults(version string) error {
	return materializeDefaults(defaultSkillsRoot(), version, false)
}

// UpdateDefaults forces a rewrite regardless of the stamp — backs
// `octo skills update`.
func UpdateDefaults(version string) error {
	return materializeDefaults(defaultSkillsRoot(), version, true)
}

func materializeDefaults(root, version string, force bool) error {
	if root == "" {
		return nil
	}
	if !force {
		if b, err := os.ReadFile(filepath.Join(root, defaultStampFile)); err == nil &&
			strings.TrimSpace(string(b)) == version {
			return nil // already current
		}
	}

	// The default root is exclusively octo-managed (users override in
	// ~/.octo/skills), so a wholesale wipe-and-rewrite is safe and keeps the
	// set in lockstep with the binary — stale skills removed, renames handled.
	// One exception predates that rule: earlier web-access versions had the
	// agent write site-experience notes inside the managed tree; rescue those
	// into the persistent sibling directory before the wipe destroys them.
	rescueSitePatterns(root)
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	if err := fs.WalkDir(defaultsFS, "defaults", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, "defaults/") // e.g. worktree-isolate/SKILL.md
		target := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		data, err := defaultsFS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	}); err != nil {
		return err
	}

	// Stamp last: if a write above failed mid-way the stamp is absent/stale, so
	// the next run retries rather than trusting a partial materialization.
	return os.WriteFile(filepath.Join(root, defaultStampFile), []byte(version), 0o644)
}

// rescueSitePatterns moves site-experience notes an earlier web-access skill
// accumulated under the managed default root (web-access/references/
// site-patterns/) into the persistent sibling directory ~/.octo/site-patterns,
// where the skill reads and writes them today. They are user data written by
// the agent, not shipped skill content, so the wipe-and-rewrite must not eat
// them. Best-effort: an existing file in the destination wins (it is the copy
// the skill has been updating since the move).
func rescueSitePatterns(root string) {
	legacy := filepath.Join(root, "web-access", "references", "site-patterns")
	entries, err := os.ReadDir(legacy)
	if err != nil {
		return
	}
	dest := filepath.Join(filepath.Dir(root), "site-patterns")
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		target := filepath.Join(dest, e.Name())
		if _, err := os.Stat(target); err == nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(legacy, e.Name()))
		if err != nil {
			continue
		}
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return
		}
		_ = os.WriteFile(target, data, 0o644)
	}
}
