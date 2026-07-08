package tools

import (
	"context"
	"fmt"
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
	n := 0
	assertProjectWorkflowsRootSeesCWDFallback(t, func(ctx context.Context) {
		n++
		if _, err := (WorkflowSaveTool{}).Execute(ctx, "c", map[string]any{
			"name":   fmt.Sprintf("ctx-wf-%d", n),
			"script": `"ok"`,
		}); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	})
}

func TestWorkflowSave_WritesParamComments(t *testing.T) {
	user, project := t.TempDir(), t.TempDir()
	useWorkflowRoots(t, user, project)

	_, err := WorkflowSaveTool{}.Execute(context.Background(), "c", map[string]any{
		"name":        "migrate",
		"script":      `agent(args["target"])`,
		"description": "Migrate files",
		"params": []any{
			map[string]any{"name": "target", "required": true, "description": "Path to migrate"},
			map[string]any{"name": "dry_run"},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(project, "migrate.rb"))
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	content := string(b)
	if !strings.Contains(content, "# @param target required Path to migrate\n") {
		t.Errorf("file = %q, want a required @param line", content)
	}
	if !strings.Contains(content, "# @param dry_run\n") {
		t.Errorf("file = %q, want an optional @param line", content)
	}

	w, ok := lookupWorkflow(context.Background(), "migrate")
	if !ok {
		t.Fatal("lookupWorkflow: not found")
	}
	if len(w.params) != 2 || w.params[0].name != "target" || !w.params[0].required || w.params[1].required {
		t.Errorf("params = %+v", w.params)
	}
}

func TestWorkflowSave_RejectsBadParamName(t *testing.T) {
	useWorkflowRoots(t, t.TempDir(), t.TempDir())
	_, err := WorkflowSaveTool{}.Execute(context.Background(), "c", map[string]any{
		"name":   "bad-param",
		"script": `"x"`,
		"params": []any{map[string]any{"name": "has space"}},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid param name") {
		t.Errorf("err = %v, want invalid-param-name error", err)
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
