package tools

import (
	"os"
	"path/filepath"
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
	if len(got) != 2 || got[0].name != "alpha" || got[1].name != "zeta" {
		t.Errorf("listWorkflows = %+v, want [alpha zeta]", got)
	}
}
