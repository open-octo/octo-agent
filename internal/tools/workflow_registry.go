package tools

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrWorkflowNotFound and ErrBuiltinWorkflow let callers (e.g. the HTTP
// handlers) distinguish DeleteWorkflow failure modes with errors.Is, rather
// than pre-checking existence with a separate lookup before calling it.
var (
	ErrWorkflowNotFound = errors.New("workflow not found")
	ErrBuiltinWorkflow  = errors.New("cannot delete a built-in workflow")
)

// defaultWorkflowsFS holds the workflows shipped with the binary — the
// curated set every install gets out of the box. discoverWorkflows seeds the
// "default" tier from this embed.FS directly, so the built-in set is always
// available even in a process that never calls MaterializeDefaultWorkflows
// (a library consumer of this package, or a test). MaterializeDefaultWorkflows
// (workflow_defaults.go) additionally writes them to ~/.octo/workflows-default
// so they're discoverable, listable and editable on disk exactly like a
// user-level workflow (mirrors internal/skills/defaults.go); when
// present, that materialized copy overlays the embedded one of the same name,
// so a local edit takes effect immediately rather than waiting for a version
// bump to re-materialize.
//
//go:embed workflow_defaults
var defaultWorkflowsFS embed.FS

// savedWorkflow is one named workflow script loaded from a registry directory.
// The script is the full file content (the @description/@param comments are
// valid Ruby and harmless to re-run).
type savedWorkflow struct {
	name        string
	description string
	params      []workflowParam
	script      string
	source      string // "default" | "user"
	path        string // on-disk file path; "" for embedded defaults
}

// workflowParam is one declared input a saved workflow expects, parsed from a
// leading `# @param <name> [required] [description]` comment line. Required
// params are checked before the workflow tool runs the script (see
// ensureRequiredWorkflowParams in workflow.go) — without this, a missing arg
// would only surface as a Ruby NoMethodError deep inside the mruby sandbox.
type workflowParam struct {
	name        string
	required    bool
	description string
}

// userWorkflowsRoot returns ~/.octo/workflows, or "" when the home dir can't be
// resolved. A var so tests can point discovery at a temp directory.
var userWorkflowsRoot = func() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".octo", "workflows")
}

// discoverWorkflows seeds the embedded default workflows, overlays the
// materialized default root (if present), then scans the user registry, and
// returns a fresh map. Precedence is embedded < materialized-default < user: a
// same-named file at a higher level overrides the one below. Safe to call
// concurrently; each call returns an independent snapshot.
func discoverWorkflows() map[string]savedWorkflow {
	fresh := make(map[string]savedWorkflow)
	scanEmbeddedWorkflows(fresh)
	scanWorkflowsRoot(defaultWorkflowsRoot(), "default", fresh)
	scanWorkflowsRoot(userWorkflowsRoot(), "user", fresh)
	return fresh
}

// scanEmbeddedWorkflows loads the binary's built-in *.rb workflows into dst.
// Their file name (without .rb) is the authoritative name, matching on-disk
// discovery so a materialized-default/user file of the same name overrides
// it.
func scanEmbeddedWorkflows(dst map[string]savedWorkflow) {
	entries, err := defaultWorkflowsFS.ReadDir("workflow_defaults")
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".rb") {
			continue
		}
		b, err := defaultWorkflowsFS.ReadFile("workflow_defaults/" + e.Name())
		if err != nil {
			continue
		}
		content := string(b)
		name := strings.TrimSuffix(e.Name(), ".rb")
		dst[name] = savedWorkflow{
			name:        name,
			description: workflowDescription(content),
			params:      workflowParams(content),
			script:      content,
			source:      "default",
		}
	}
}

// scanWorkflowsRoot reads *.rb workflow scripts from root into dst (existing
// keys are overwritten), tagging each with source ("default" or "user"). A
// missing or unreadable root is a no-op.
func scanWorkflowsRoot(root, source string, dst map[string]savedWorkflow) {
	if root == "" {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".rb") {
			continue
		}
		path := filepath.Join(root, name)
		w, ok := parseWorkflowFile(path)
		if !ok {
			continue
		}
		// The file name (without .rb) is the authoritative workflow name.
		w.name = strings.TrimSuffix(name, ".rb")
		w.source = source
		w.path = path
		dst[w.name] = w
	}
}

// parseWorkflowFile reads one .rb workflow script. The whole file is the script;
// the description comes from a leading `# @description ...` line, falling back to
// the first non-empty `#` comment line, and declared params come from leading
// `# @param ...` lines. ok is false only when the file can't be read.
func parseWorkflowFile(path string) (savedWorkflow, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return savedWorkflow{}, false
	}
	content := string(b)
	return savedWorkflow{
		description: workflowDescription(content),
		params:      workflowParams(content),
		script:      content,
	}, true
}

// leadingComments returns the body text of each leading `#` comment line in
// script (its header block), stopping at the first blank-trimmed line that
// isn't a comment.
func leadingComments(script string) []string {
	var out []string
	for _, ln := range strings.Split(script, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if !strings.HasPrefix(t, "#") {
			break // first line of real code: no more leading comments
		}
		out = append(out, strings.TrimSpace(strings.TrimPrefix(t, "#")))
	}
	return out
}

// workflowDescription extracts a one-line description from a script's leading
// comments: the `# @description ...` line if present, else the first non-empty
// `#` comment line. Empty when neither exists.
func workflowDescription(script string) string {
	first := ""
	for _, body := range leadingComments(script) {
		if d := strings.TrimSpace(strings.TrimPrefix(body, "@description")); d != body {
			return d
		}
		if first == "" {
			first = body
		}
	}
	return first
}

// workflowParams extracts `# @param <name> [required][: <description>]`
// declarations from a script's leading comment block, written by
// formatWorkflowParamComment (workflow_save.go). The colon is load-bearing:
// everything before it is pure grammar (name, then an optional literal
// "required" token), everything after is free-text description, so a
// description that itself starts with the word "required" (e.g. "required
// command to double check") can never be misparsed as the required flag —
// unlike a naive token-stream split, which would (and would also eat that
// word out of the description).
func workflowParams(script string) []workflowParam {
	var out []workflowParam
	for _, body := range leadingComments(script) {
		// Word-boundary check: "@param" must be the whole line or followed by
		// whitespace, so a line like "@parameterized ..." (which merely starts
		// with the same runes) isn't misread as a parameter declaration.
		if body != "@param" && !strings.HasPrefix(body, "@param ") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(body, "@param"))
		if rest == "" {
			continue
		}
		head, description := rest, ""
		if idx := strings.Index(rest, ":"); idx >= 0 {
			head = strings.TrimSpace(rest[:idx])
			description = strings.TrimSpace(rest[idx+1:])
		}
		fields := strings.Fields(head)
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		required := len(fields) > 1 && fields[1] == "required"
		out = append(out, workflowParam{name: name, required: required, description: description})
	}
	return out
}

// lookupWorkflow returns the named workflow, scanning the registry fresh so a
// just-authored file is picked up without a restart.
func lookupWorkflow(name string) (savedWorkflow, bool) {
	workflows := discoverWorkflows()
	w, ok := workflows[name]
	return w, ok
}

// listWorkflows returns every named workflow, sorted by name, scanning fresh.
func listWorkflows() []savedWorkflow {
	workflows := discoverWorkflows()
	out := make([]savedWorkflow, 0, len(workflows))
	for _, w := range workflows {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// NamedWorkflow is a public, script-free view of a registered workflow for API
// surfaces (the web discovery panel). It deliberately omits the script body.
type NamedWorkflow struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"` // "default" | "user"
}

// ListNamedWorkflows returns every registered workflow (embedded defaults +
// user), sorted by name, as a public view for the web panel.
func ListNamedWorkflows() []NamedWorkflow {
	saved := listWorkflows()
	out := make([]NamedWorkflow, 0, len(saved))
	for _, w := range saved {
		out = append(out, NamedWorkflow{Name: w.name, Description: w.description, Source: w.source})
	}
	return out
}

// WorkflowDetail is the full view of one workflow, including its script, for
// the web management panel's view-source and export actions.
type WorkflowDetail struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Script      string `json:"script"`
}

// GetNamedWorkflow returns the full detail (including script) of one
// registered workflow.
func GetNamedWorkflow(name string) (WorkflowDetail, bool) {
	w, ok := lookupWorkflow(name)
	if !ok {
		return WorkflowDetail{}, false
	}
	return WorkflowDetail{Name: w.name, Description: w.description, Source: w.source, Script: w.script}, true
}

// DeleteWorkflow removes a user-level workflow's on-disk file. Embedded
// default workflows cannot be deleted. The file is moved to trash, not
// permanently removed.
func DeleteWorkflow(name string) error {
	w, ok := lookupWorkflow(name)
	if !ok {
		return fmt.Errorf("workflow %q: %w", name, ErrWorkflowNotFound)
	}
	if w.source == "default" {
		return fmt.Errorf("workflow %q: %w", name, ErrBuiltinWorkflow)
	}
	if w.path == "" {
		return fmt.Errorf("workflow %q has no on-disk file", name)
	}
	if err := os.Remove(w.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove workflow file %s: %w", w.path, err)
	}
	return nil
}

// UserWorkflowsRoot exports the on-disk root of user-level saved workflows,
// for `octo workflows path`.
func UserWorkflowsRoot() string { return userWorkflowsRoot() }
