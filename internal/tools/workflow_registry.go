package tools

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/open-octo/octo-agent/internal/memory"
	"github.com/open-octo/octo-agent/internal/trash"
)

// defaultWorkflowsFS holds the workflows shipped with the binary — the curated
// set every install gets out of the box. Unlike default skills they are not
// materialized to disk: discoverWorkflows merges them in-memory as the lowest
// priority on every read, so a same-named user- or project-level file
// transparently overrides one. `octo workflows` and the web panel read this
// same in-memory merge (via ListNamedWorkflows/GetNamedWorkflow) rather than
// scanning a materialized directory, so there's nothing to keep in sync.
//
//go:embed workflow_defaults
var defaultWorkflowsFS embed.FS

// savedWorkflow is one named workflow script loaded from a registry directory.
// The script is the full file content (the @description comment is valid Ruby
// and harmless to re-run).
type savedWorkflow struct {
	name        string
	description string
	script      string
	source      string // "default" | "user" | "project"
	path        string // on-disk file path; "" for embedded defaults
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

// projectWorkflowsRoot returns <project-root>/.octo/workflows for the given
// working directory, or "" when it can't be resolved. Project-level workflows
// override user-level ones of the same name (matching .octo/agents semantics).
// A var so tests can point discovery at a temp directory.
//
// Every current caller pre-resolves cwd via WorkingDirOrCWD before calling
// this, so the os.Getwd() fallback below is normally unreachable — it's kept
// as defense in depth for any future or direct caller that passes "" without
// going through that helper first (os.Getwd() itself failing is the only way
// today's callers would still hit it).
var projectWorkflowsRoot = func(cwd string) string {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil || cwd == "" {
			return ""
		}
	}
	root := memory.ProjectRoot(cwd)
	if root == "" {
		return ""
	}
	return filepath.Join(root, ".octo", "workflows")
}

// discoverWorkflows seeds the embedded default workflows, then scans the user-
// and project-level registries, and returns a fresh map. Precedence is
// embedded < user < project: a same-named file at a higher level overrides the
// one below. cwd is the working directory used to resolve the project-level
// registry; when empty it falls back to the process CWD. Safe to call
// concurrently; each call returns an independent snapshot.
func discoverWorkflows(cwd string) map[string]savedWorkflow {
	fresh := make(map[string]savedWorkflow)
	scanEmbeddedWorkflows(fresh)
	scanWorkflowsRoot(userWorkflowsRoot(), "user", fresh)
	scanWorkflowsRoot(projectWorkflowsRoot(cwd), "project", fresh)
	return fresh
}

// scanEmbeddedWorkflows loads the binary's built-in *.rb workflows into dst.
// Their file name (without .rb) is the authoritative name, matching on-disk
// discovery so a user/project file of the same name overrides the default.
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
			script:      content,
			source:      "default",
		}
	}
}

// scanWorkflowsRoot reads *.rb workflow scripts from root into dst (existing
// keys are overwritten), tagging each with source ("user" or "project"). A
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
// the first non-empty `#` comment line. ok is false only when the file can't be
// read.
func parseWorkflowFile(path string) (savedWorkflow, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return savedWorkflow{}, false
	}
	content := string(b)
	return savedWorkflow{
		description: workflowDescription(content),
		script:      content,
	}, true
}

// workflowDescription extracts a one-line description from a script's leading
// comments: the `# @description ...` line if present, else the first non-empty
// `#` comment line. Empty when neither exists.
func workflowDescription(script string) string {
	first := ""
	for _, ln := range strings.Split(script, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if !strings.HasPrefix(t, "#") {
			break // first line of real code: no more leading comments
		}
		body := strings.TrimSpace(strings.TrimPrefix(t, "#"))
		if d := strings.TrimSpace(strings.TrimPrefix(body, "@description")); d != body {
			return d
		}
		if first == "" {
			first = body
		}
	}
	return first
}

// lookupWorkflow returns the named workflow, scanning the registries fresh so a
// just-authored file is picked up without a restart. The project-level registry
// is resolved from ctx's working directory when present (e.g. a cron task's
// directory), otherwise from the process CWD.
func lookupWorkflow(ctx context.Context, name string) (savedWorkflow, bool) {
	workflows := discoverWorkflows(WorkingDirOrCWD(ctx))
	w, ok := workflows[name]
	return w, ok
}

// listWorkflows returns every named workflow, sorted by name, scanning fresh.
// The project-level registry is resolved from ctx's working directory when
// present, otherwise from the process CWD.
func listWorkflows(ctx context.Context) []savedWorkflow {
	workflows := discoverWorkflows(WorkingDirOrCWD(ctx))
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
	Source      string `json:"source"` // "default" | "user" | "project"
}

// ListNamedWorkflows returns every registered workflow (embedded defaults +
// user + project), sorted by name, as a public view for the web panel.
// Project-level workflows are resolved from ActiveWorkflowDiscoveryCWD when a
// turn has stamped one (see workflow.go), otherwise the process CWD.
func ListNamedWorkflows() []NamedWorkflow {
	saved := listWorkflows(WithWorkingDir(context.Background(), ActiveWorkflowDiscoveryCWD()))
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
// registered workflow, resolved the same way ListNamedWorkflows is.
func GetNamedWorkflow(name string) (WorkflowDetail, bool) {
	w, ok := lookupWorkflow(WithWorkingDir(context.Background(), ActiveWorkflowDiscoveryCWD()), name)
	if !ok {
		return WorkflowDetail{}, false
	}
	return WorkflowDetail{Name: w.name, Description: w.description, Source: w.source, Script: w.script}, true
}

// DeleteWorkflow removes a user- or project-level workflow's on-disk file,
// resolved the same way ListNamedWorkflows is. Embedded default workflows
// cannot be deleted. The file is moved to trash, not permanently removed.
func DeleteWorkflow(name string) error {
	w, ok := lookupWorkflow(WithWorkingDir(context.Background(), ActiveWorkflowDiscoveryCWD()), name)
	if !ok {
		return fmt.Errorf("workflow %q not found", name)
	}
	if w.source == "default" {
		return fmt.Errorf("cannot delete built-in workflow %q", name)
	}
	if w.path == "" {
		return fmt.Errorf("workflow %q has no on-disk file", name)
	}
	if err := trash.Move(w.path, filepath.Dir(w.path)); err != nil {
		return fmt.Errorf("trash workflow file %s: %w", w.path, err)
	}
	return nil
}

// UserWorkflowsRoot exports the on-disk root of user-level saved workflows,
// for `octo workflows path`.
func UserWorkflowsRoot() string { return userWorkflowsRoot() }

// ProjectWorkflowsRoot exports the on-disk root of project-level saved
// workflows for the given working directory, for `octo workflows path`.
func ProjectWorkflowsRoot(cwd string) string { return projectWorkflowsRoot(cwd) }
