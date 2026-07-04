package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowSave_ProjectScopeRoundTrips(t *testing.T) {
	user, project := t.TempDir(), t.TempDir()
	useWorkflowRoots(t, user, project)

	res, err := WorkflowSaveTool{}.Execute(context.Background(), "c", map[string]any{
		"name":        "bug-hunt",
		"script":      `agent(args["q"])`,
		"description": "Find and verify bugs",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Text, "Saved") {
		t.Errorf("result = %q", res.Text)
	}

	// Default scope is project: the file lands in the project root.
	b, err := os.ReadFile(filepath.Join(project, "bug-hunt.rb"))
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if !strings.HasPrefix(string(b), "# @description Find and verify bugs\n") {
		t.Errorf("file = %q, want @description header", string(b))
	}

	// And it's immediately resolvable by name.
	w, ok := lookupWorkflow(context.Background(), "bug-hunt")
	if !ok || w.description != "Find and verify bugs" {
		t.Errorf("lookupWorkflow = %+v, ok = %v", w, ok)
	}
}

func TestWorkflowSave_ProjectScopeUsesContextWorkingDir(t *testing.T) {
	// When the server process is not in the project directory but the context
	// carries a working directory, project-scope saves should land in the
	// context's project root, not the process CWD.
	project := t.TempDir()

	ou, op := userWorkflowsRoot, projectWorkflowsRoot
	var seenCWD string
	userWorkflowsRoot = func() string { return "" }
	projectWorkflowsRoot = func(cwd string) string {
		seenCWD = cwd
		return project
	}
	t.Cleanup(func() { userWorkflowsRoot, projectWorkflowsRoot = ou, op })

	ctx := WithWorkingDir(context.Background(), project)
	if _, err := (WorkflowSaveTool{}).Execute(ctx, "c", map[string]any{
		"name":   "ctx-wf",
		"script": `"ok"`,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if seenCWD != project {
		t.Errorf("projectWorkflowsRoot got cwd %q, want %q", seenCWD, project)
	}

	// Without context working dir, falls back to process CWD.
	seenCWD = ""
	otherDir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(otherDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(origDir); err != nil {
			t.Error(err)
		}
	})
	if _, err := (WorkflowSaveTool{}).Execute(context.Background(), "c", map[string]any{
		"name":   "fallback-wf",
		"script": `"ok"`,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	resolvedSeen, err := filepath.EvalSymlinks(seenCWD)
	if err != nil {
		t.Fatal(err)
	}
	resolvedOther, err := filepath.EvalSymlinks(otherDir)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedSeen != resolvedOther {
		t.Errorf("projectWorkflowsRoot fallback cwd = %q, want %q", resolvedSeen, resolvedOther)
	}
}

func TestWorkflowSave_UserScope(t *testing.T) {
	user, project := t.TempDir(), t.TempDir()
	useWorkflowRoots(t, user, project)

	if _, err := (WorkflowSaveTool{}).Execute(context.Background(), "c", map[string]any{
		"name":   "shared",
		"script": `"x"`,
		"scope":  "user",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(user, "shared.rb")); err != nil {
		t.Errorf("expected file in user root: %v", err)
	}
}

func TestWorkflowSave_RejectsBadName(t *testing.T) {
	useWorkflowRoots(t, t.TempDir(), t.TempDir())
	for _, bad := range []string{"../escape", "Has Space", "UPPER", ""} {
		_, err := WorkflowSaveTool{}.Execute(context.Background(), "c", map[string]any{
			"name":   bad,
			"script": `"x"`,
		})
		if err == nil || !strings.Contains(err.Error(), "invalid name") {
			t.Errorf("name %q: err = %v, want invalid-name", bad, err)
		}
	}
}

func TestWorkflowSave_OverwriteReported(t *testing.T) {
	user, project := t.TempDir(), t.TempDir()
	useWorkflowRoots(t, user, project)
	args := map[string]any{"name": "dup", "script": `"x"`}

	if _, err := (WorkflowSaveTool{}).Execute(context.Background(), "c", args); err != nil {
		t.Fatal(err)
	}
	res, err := WorkflowSaveTool{}.Execute(context.Background(), "c", args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "Overwrote") {
		t.Errorf("result = %q, want Overwrote on second save", res.Text)
	}
}
