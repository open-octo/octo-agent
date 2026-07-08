package tools

import (
	"os"
	"path/filepath"
	"strings"
)

// defaultWorkflowsRoot returns ~/.octo/workflows-default — a dedicated,
// octo-managed directory kept separate from ~/.octo/workflows so refreshing
// the defaults never touches a user's own saved workflows. A var so tests can
// redirect it. Mirrors internal/skills/defaults.go's defaultSkillsRoot.
var defaultWorkflowsRoot = func() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".octo", "workflows-default")
}

// DefaultWorkflowsRoot is the on-disk location of the materialized default
// workflows (~/.octo/workflows-default), exported for `octo workflows path`.
func DefaultWorkflowsRoot() string { return defaultWorkflowsRoot() }

// defaultWorkflowStampFile records which binary version last materialized the
// default workflows, so MaterializeDefaultWorkflows can no-op until the
// version changes.
const defaultWorkflowStampFile = ".octo-version"

// MaterializeDefaultWorkflows writes the embedded default workflows to
// ~/.octo/workflows-default when the on-disk version stamp doesn't match
// version, so they're discoverable, listable and overridable on disk like any
// saved workflow, instead of only living inside the binary. It's a fast no-op
// once the install is current (a single stamp read). Best-effort: the caller
// should ignore the error so a read-only HOME never blocks a session.
func MaterializeDefaultWorkflows(version string) error {
	return materializeDefaultWorkflows(defaultWorkflowsRoot(), version, false)
}

// UpdateDefaultWorkflows forces a rewrite regardless of the stamp — backs
// `octo workflows update`.
func UpdateDefaultWorkflows(version string) error {
	return materializeDefaultWorkflows(defaultWorkflowsRoot(), version, true)
}

func materializeDefaultWorkflows(root, version string, force bool) error {
	if root == "" {
		return nil
	}
	if !force {
		if b, err := os.ReadFile(filepath.Join(root, defaultWorkflowStampFile)); err == nil &&
			strings.TrimSpace(string(b)) == version {
			return nil // already current
		}
	}

	// The default root is exclusively octo-managed (users override in
	// ~/.octo/workflows), so a wholesale wipe-and-rewrite is safe and keeps the
	// set in lockstep with the binary — stale workflows removed, renames handled.
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	entries, err := defaultWorkflowsFS.ReadDir("workflow_defaults")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".rb") {
			continue
		}
		data, err := defaultWorkflowsFS.ReadFile("workflow_defaults/" + e.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, e.Name()), data, 0o644); err != nil {
			return err
		}
	}

	// Stamp last: if a write above failed mid-way the stamp is absent/stale, so
	// the next run retries rather than trusting a partial materialization.
	return os.WriteFile(filepath.Join(root, defaultWorkflowStampFile), []byte(version), 0o644)
}
