// Package prompt assembles the agent's system prompt from layered sources.
//
// The composed prompt is meant to be built ONCE per session and frozen: the
// provider caches the system+tools prefix (see internal/provider/anthropic),
// and recomputing the prompt mid-session would invalidate that cache for
// every subsequent turn. Compose at session start, set it on Agent.System,
// and don't touch it again.
package prompt

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"
)

// base is the built-in foundation prompt: octo's identity plus the
// tool-use, permission, and read-before-write norms. Embedded so it ships
// in the single binary.
//
//go:embed base.md
var base string

// ProjectContextFile is the per-repo conventions file Compose looks for in
// the working directory. It carries project-specific rules the agent should
// follow (the human-facing counterpart to CLAUDE.md).
const ProjectContextFile = ".octorules"

// Compose assembles the session system prompt from up to three layers, in
// order of increasing specificity:
//
//  1. base    — embedded octo foundation (always present)
//  2. project — ProjectContextFile in cwd, if present (repo conventions)
//  3. user    — the --system value, if any (highest-priority override, last)
//
// Empty layers are skipped. Later layers appear later in the text, which is
// the conventional way to let more specific instructions take precedence.
func Compose(userSystem, cwd string) string {
	layers := []string{strings.TrimSpace(base)}

	if proj := readProjectContext(cwd); proj != "" {
		layers = append(layers, "# Project conventions ("+ProjectContextFile+")\n\n"+proj)
	}
	if u := strings.TrimSpace(userSystem); u != "" {
		layers = append(layers, u)
	}

	return strings.Join(layers, "\n\n---\n\n")
}

// readProjectContext returns the trimmed contents of ProjectContextFile in
// cwd, or "" if it's absent/unreadable/empty. A missing file is not an error
// — most directories won't have one.
func readProjectContext(cwd string) string {
	if cwd == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(cwd, ProjectContextFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
