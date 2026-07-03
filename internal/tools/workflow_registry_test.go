package tools

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// useWorkflowRoots points the user/project registries at temp dirs for the test.
func useWorkflowRoots(t *testing.T, userDir, projectDir string) {
	t.Helper()
	ou, op := userWorkflowsRoot, projectWorkflowsRoot
	userWorkflowsRoot = func() string { return userDir }
	projectWorkflowsRoot = func() string { return projectDir }
	t.Cleanup(func() { userWorkflowsRoot, projectWorkflowsRoot = ou, op })
}

func writeWorkflowFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLookupWorkflow_LoadsAndParsesDescription(t *testing.T) {
	user := t.TempDir()
	useWorkflowRoots(t, user, "")
	writeWorkflowFile(t, user, "bug-hunt.rb", "# @description Find and verify bugs\nagent(args[\"q\"])\n")

	w, ok := lookupWorkflow("bug-hunt")
	if !ok {
		t.Fatal("lookupWorkflow: not found")
	}
	if w.description != "Find and verify bugs" {
		t.Errorf("description = %q", w.description)
	}
	if w.script == "" || w.name != "bug-hunt" {
		t.Errorf("workflow = %+v", w)
	}
}

func TestWorkflowDescription_FallsBackToFirstComment(t *testing.T) {
	if got := workflowDescription("# just a note\n# second line\nagent(\"x\")"); got != "just a note" {
		t.Errorf("description = %q, want first comment", got)
	}
	if got := workflowDescription("agent(\"x\")"); got != "" {
		t.Errorf("description = %q, want empty when no leading comment", got)
	}
}

func TestLookupWorkflow_ProjectOverridesUser(t *testing.T) {
	user, project := t.TempDir(), t.TempDir()
	useWorkflowRoots(t, user, project)
	writeWorkflowFile(t, user, "dup.rb", "# @description user version\n\"u\"\n")
	writeWorkflowFile(t, project, "dup.rb", "# @description project version\n\"p\"\n")

	w, ok := lookupWorkflow("dup")
	if !ok || w.description != "project version" {
		t.Errorf("workflow = %+v, ok = %v; want project version to win", w, ok)
	}
}

func TestLookupWorkflow_UnknownName(t *testing.T) {
	useWorkflowRoots(t, t.TempDir(), t.TempDir())
	if _, ok := lookupWorkflow("nope"); ok {
		t.Error("lookupWorkflow returned ok for unknown name")
	}
}

func TestListWorkflows_SortedUnionOfRoots(t *testing.T) {
	user, project := t.TempDir(), t.TempDir()
	useWorkflowRoots(t, user, project)
	writeWorkflowFile(t, user, "zeta.rb", "\"z\"\n")
	writeWorkflowFile(t, project, "alpha.rb", "\"a\"\n")
	writeWorkflowFile(t, user, "ignored.txt", "not a workflow")

	got := listWorkflows()
	names := make([]string, len(got))
	for i, w := range got {
		names[i] = w.name
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("listWorkflows not sorted: %v", names)
	}
	// The union includes the on-disk files plus every embedded default.
	for _, want := range []string{"alpha", "zeta"} {
		if !containsName(names, want) {
			t.Errorf("listWorkflows = %v, missing %q", names, want)
		}
	}
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func TestLookupWorkflow_EmbeddedDefaultAlwaysAvailable(t *testing.T) {
	// No user/project roots: every built-in preset must still resolve.
	useWorkflowRoots(t, "", "")
	for _, name := range []string{"adversarial-review", "parallel-understand", "batch-migrate", "daily-triage"} {
		w, ok := lookupWorkflow(name)
		if !ok {
			t.Errorf("embedded default %q not found", name)
			continue
		}
		if w.script == "" || w.description == "" {
			t.Errorf("embedded workflow %q = %+v, want non-empty script and description", name, w)
		}
	}
}

func TestLookupWorkflow_UserOverridesEmbeddedDefault(t *testing.T) {
	user := t.TempDir()
	useWorkflowRoots(t, user, "")
	writeWorkflowFile(t, user, "adversarial-review.rb", "# @description my override\n\"x\"\n")

	w, ok := lookupWorkflow("adversarial-review")
	if !ok || w.description != "my override" {
		t.Errorf("workflow = %+v, ok = %v; want the user file to override the embedded default", w, ok)
	}
}
