package agentprofile

import (
	"embed"
	"os"
	"path/filepath"
	"strings"
)

// defaultsFS holds the curated expert profiles shipped with the binary — the
// officially-maintained personas every install gets out of the box.
// Embedding (rather than downloading) keeps a fresh install offline-capable
// and version-locked to the binary — no network, no supply chain. Mirrors
// internal/skills/defaults.go; a flat layout (not skills' nested SKILL.md
// dirs) since a profile is already a single <id>.md file.
//
//go:embed defaults
var defaultsFS embed.FS

// defaultStampFile records which binary version last materialized the default
// agents, so MaterializeDefaults can no-op until the version changes.
const defaultStampFile = ".octo-version"

// defaultAgentsRoot returns ~/.octo/agents-default — a dedicated, octo-managed
// directory kept separate from ~/.octo/agents so refreshing the curated
// experts never touches a user's own saved agents. A var so tests can
// redirect it.
var defaultAgentsRoot = func() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".octo", "agents-default")
}

// DefaultRoot is the on-disk location of the materialized curated experts
// (~/.octo/agents-default), exported for `octo agents path`.
func DefaultRoot() string { return defaultAgentsRoot() }

// MaterializeDefaults writes the embedded curated experts to the default root
// when the on-disk version stamp doesn't match version. It's a fast no-op
// once the install is current (a single stamp read). Best-effort: the caller
// should ignore the error so a read-only HOME never blocks a session.
func MaterializeDefaults(version string) error {
	return materializeDefaults(defaultAgentsRoot(), version, false)
}

// UpdateDefaults forces a rewrite regardless of the stamp — backs
// `octo agents update`.
func UpdateDefaults(version string) error {
	return materializeDefaults(defaultAgentsRoot(), version, true)
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
	// ~/.octo/agents), so a wholesale wipe-and-rewrite is safe and keeps the
	// set in lockstep with the binary — retired personas removed, renames and
	// content edits handled.
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	entries, err := defaultsFS.ReadDir("defaults")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := defaultsFS.ReadFile("defaults/" + e.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, e.Name()), data, 0o644); err != nil {
			return err
		}
	}

	// Stamp last: if a write above failed mid-way the stamp is absent/stale, so
	// the next run retries rather than trusting a partial materialization.
	return os.WriteFile(filepath.Join(root, defaultStampFile), []byte(version), 0o644)
}
