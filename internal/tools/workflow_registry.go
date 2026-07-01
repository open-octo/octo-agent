package tools

import (
	"embed"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/open-octo/octo-agent/internal/memory"
)

// defaultWorkflowsFS holds the workflows shipped with the binary — the curated
// set every install gets out of the box. Unlike default skills they are not
// materialized to disk (there is no on-disk workflows CLI to list/edit them):
// discoverWorkflows merges them in-memory as the lowest priority, so a
// same-named user- or project-level file transparently overrides one.
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

// projectWorkflowsRoot returns <project-root>/.octo/workflows for the current
// working directory's repository, or "" when it can't be resolved. Project-level
// workflows override user-level ones of the same name (matching .octo/agents
// semantics). A var so tests can point it at a temp directory.
var projectWorkflowsRoot = func() string {
	cwd, err := os.Getwd()
	if err != nil || cwd == "" {
		return ""
	}
	root := memory.ProjectRoot(cwd)
	if root == "" {
		return ""
	}
	return filepath.Join(root, ".octo", "workflows")
}

// discoveredWorkflows holds the last scanned named workflows.
var (
	discoveredWorkflowsMu sync.RWMutex
	discoveredWorkflows   map[string]savedWorkflow
)

// discoverWorkflows seeds the embedded default workflows, then scans the user-
// and project-level registries, refreshing the package-level cache. Precedence
// is embedded < user < project: a same-named file at a higher level overrides
// the one below. Safe to call concurrently; callers that need the freshest set
// call it before lookupWorkflow / listWorkflows.
func discoverWorkflows() {
	fresh := make(map[string]savedWorkflow)
	scanEmbeddedWorkflows(fresh)
	for _, root := range []string{userWorkflowsRoot(), projectWorkflowsRoot()} {
		scanWorkflowsRoot(root, fresh)
	}
	discoveredWorkflowsMu.Lock()
	discoveredWorkflows = fresh
	discoveredWorkflowsMu.Unlock()
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
		}
	}
}

// scanWorkflowsRoot reads *.rb workflow scripts from root into dst (existing
// keys are overwritten). A missing or unreadable root is a no-op.
func scanWorkflowsRoot(root string, dst map[string]savedWorkflow) {
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
		w, ok := parseWorkflowFile(filepath.Join(root, name))
		if !ok {
			continue
		}
		// The file name (without .rb) is the authoritative workflow name.
		w.name = strings.TrimSuffix(name, ".rb")
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
// just-authored file is picked up without a restart.
func lookupWorkflow(name string) (savedWorkflow, bool) {
	discoverWorkflows()
	discoveredWorkflowsMu.RLock()
	defer discoveredWorkflowsMu.RUnlock()
	w, ok := discoveredWorkflows[name]
	return w, ok
}

// listWorkflows returns every named workflow, sorted by name, scanning fresh.
func listWorkflows() []savedWorkflow {
	discoverWorkflows()
	discoveredWorkflowsMu.RLock()
	defer discoveredWorkflowsMu.RUnlock()
	out := make([]savedWorkflow, 0, len(discoveredWorkflows))
	for _, w := range discoveredWorkflows {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}
