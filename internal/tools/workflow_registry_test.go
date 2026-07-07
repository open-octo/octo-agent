package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/open-octo/octo-agent/internal/trash"
)

// useWorkflowRoots points the user/project registries at temp dirs for the test.
func useWorkflowRoots(t *testing.T, userDir, projectDir string) {
	t.Helper()
	ou, op := userWorkflowsRoot, projectWorkflowsRoot
	userWorkflowsRoot = func() string { return userDir }
	projectWorkflowsRoot = func(_ string) string { return projectDir }
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

	w, ok := lookupWorkflow(context.Background(), "bug-hunt")
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

	w, ok := lookupWorkflow(context.Background(), "dup")
	if !ok || w.description != "project version" {
		t.Errorf("workflow = %+v, ok = %v; want project version to win", w, ok)
	}
}

// When cwd is the home directory itself, projectWorkflowsRoot falls back to
// cwd and resolves to the exact same path as userWorkflowsRoot. Workflows
// there must stay labeled "user", not get relabeled "project" by scanning
// the same directory a second time.
func TestLookupWorkflow_SameUserAndProjectRootStaysUser(t *testing.T) {
	home := t.TempDir()
	useWorkflowRoots(t, home, home)
	writeWorkflowFile(t, home, "solo.rb", "# @description only copy\n\"x\"\n")

	w, ok := lookupWorkflow(context.Background(), "solo")
	if !ok {
		t.Fatal("lookupWorkflow: not found")
	}
	if w.source != "user" {
		t.Errorf("source = %q, want user (cwd == home must not double-count as project)", w.source)
	}
}

func TestLookupWorkflow_UnknownName(t *testing.T) {
	useWorkflowRoots(t, t.TempDir(), t.TempDir())
	if _, ok := lookupWorkflow(context.Background(), "nope"); ok {
		t.Error("lookupWorkflow returned ok for unknown name")
	}
}

func TestListWorkflows_SortedUnionOfRoots(t *testing.T) {
	user, project := t.TempDir(), t.TempDir()
	useWorkflowRoots(t, user, project)
	writeWorkflowFile(t, user, "zeta.rb", "\"z\"\n")
	writeWorkflowFile(t, project, "alpha.rb", "\"a\"\n")
	writeWorkflowFile(t, user, "ignored.txt", "not a workflow")

	got := listWorkflows(context.Background())
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
	for _, name := range []string{"parallel-understand", "batch-migrate", "daily-triage"} {
		w, ok := lookupWorkflow(context.Background(), name)
		if !ok {
			t.Errorf("embedded default %q not found", name)
			continue
		}
		if w.script == "" || w.description == "" {
			t.Errorf("embedded workflow %q = %+v, want non-empty script and description", name, w)
		}
	}
}

func TestLookupWorkflow_ReferenceTemplatesNotEmbedded(t *testing.T) {
	// The loop-engineering skill ships several workflow scripts as reference
	// templates under its own templates/ dir (read + adapted on demand, or
	// saved with workflow_save) rather than as embedded registry defaults —
	// only daily-triage graduated to a built-in preset. This guards against
	// silently re-adding them to workflow_defaults/ without a deliberate call.
	useWorkflowRoots(t, "", "")
	for _, name := range []string{"issue-triage", "pr-babysitter", "ci-sweeper", "dependency-sweeper", "changelog-drafter", "post-merge-cleanup"} {
		if _, ok := lookupWorkflow(context.Background(), name); ok {
			t.Errorf("%q resolved as an embedded default; expected it to be a loop-engineering reference template only", name)
		}
	}
}

func TestLookupWorkflow_UserOverridesEmbeddedDefault(t *testing.T) {
	user := t.TempDir()
	useWorkflowRoots(t, user, "")
	writeWorkflowFile(t, user, "batch-migrate.rb", "# @description my override\n\"x\"\n")

	w, ok := lookupWorkflow(context.Background(), "batch-migrate")
	if !ok || w.description != "my override" {
		t.Errorf("workflow = %+v, ok = %v; want the user file to override the embedded default", w, ok)
	}
}

// assertProjectWorkflowsRootSeesCWDFallback verifies that invoking fn with a
// WithWorkingDir-stamped ctx resolves the project-level registry from that
// stamped directory, and from the process CWD when ctx carries none — the
// shared "does this ctx-aware entrypoint fall back correctly" check needed by
// both lookupWorkflow (workflow_registry_test.go) and WorkflowSaveTool.Execute
// (workflow_save_test.go), which each depend on the exact same guarantee.
func assertProjectWorkflowsRootSeesCWDFallback(t *testing.T, fn func(ctx context.Context)) {
	t.Helper()
	ou, op := userWorkflowsRoot, projectWorkflowsRoot
	var seenCWD string
	userWorkflowsRoot = func() string { return "" }
	projectWorkflowsRoot = func(cwd string) string {
		seenCWD = cwd
		return t.TempDir() // any writable root; its content isn't asserted here
	}
	t.Cleanup(func() { userWorkflowsRoot, projectWorkflowsRoot = ou, op })

	stamped := t.TempDir()
	fn(WithWorkingDir(context.Background(), stamped))
	if seenCWD != stamped {
		t.Errorf("projectWorkflowsRoot got cwd %q, want the stamped dir %q", seenCWD, stamped)
	}

	// Without a stamped working dir, the process CWD fallback should be passed.
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
	fn(context.Background())
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

func TestLookupWorkflow_UsesContextWorkingDir(t *testing.T) {
	// When the process CWD is not inside the project but the context carries a
	// working directory, lookupWorkflow should resolve project-level workflows
	// from that directory (content-parsing itself is already covered by
	// TestLookupWorkflow_LoadsAndParsesDescription; this test is only about
	// which directory gets used).
	assertProjectWorkflowsRootSeesCWDFallback(t, func(ctx context.Context) {
		lookupWorkflow(ctx, "whatever")
	})
}

func TestGetNamedWorkflow_FoundAndNotFound(t *testing.T) {
	user := t.TempDir()
	useWorkflowRoots(t, user, "")
	writeWorkflowFile(t, user, "release-notes.rb", "# @description Draft release notes\n\"ok\"\n")

	detail, ok := GetNamedWorkflow("release-notes")
	if !ok {
		t.Fatal("GetNamedWorkflow: not found")
	}
	if detail.Name != "release-notes" || detail.Description != "Draft release notes" || detail.Source != "user" || detail.Script == "" {
		t.Errorf("detail = %+v", detail)
	}

	if _, ok := GetNamedWorkflow("does-not-exist"); ok {
		t.Error("GetNamedWorkflow returned ok for an unknown name")
	}
}

func TestDeleteWorkflow_RefusesBuiltin(t *testing.T) {
	useWorkflowRoots(t, "", "")
	err := DeleteWorkflow("batch-migrate")
	if err == nil {
		t.Fatal("DeleteWorkflow: want error deleting a built-in workflow, got nil")
	}
	if !errors.Is(err, ErrBuiltinWorkflow) {
		t.Errorf("err = %v, want errors.Is(err, ErrBuiltinWorkflow)", err)
	}
}

func TestDeleteWorkflow_UnknownName(t *testing.T) {
	useWorkflowRoots(t, t.TempDir(), t.TempDir())
	err := DeleteWorkflow("does-not-exist")
	if err == nil {
		t.Fatal("DeleteWorkflow: want error for an unknown name, got nil")
	}
	if !errors.Is(err, ErrWorkflowNotFound) {
		t.Errorf("err = %v, want errors.Is(err, ErrWorkflowNotFound)", err)
	}
}

func TestDeleteWorkflow_RemovesUserFileAndTrashesIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	user := t.TempDir()
	useWorkflowRoots(t, user, "")
	writeWorkflowFile(t, user, "scratch.rb", "# @description Throwaway\n\"ok\"\n")
	path := filepath.Join(user, "scratch.rb")

	if err := DeleteWorkflow("scratch"); err != nil {
		t.Fatalf("DeleteWorkflow: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("workflow file still exists on disk after delete: err = %v", err)
	}
	if _, ok := GetNamedWorkflow("scratch"); ok {
		t.Error("deleted workflow still resolves via GetNamedWorkflow")
	}

	// Trashed, not permanently destroyed — mirrors skills.Registry.Delete.
	trashed, err := trash.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range trashed {
		if e.Original == path {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("deleted workflow file not found in trash: entries = %+v", trashed)
	}
}

func TestUserAndProjectWorkflowsRoot_ExportPrivateResolvers(t *testing.T) {
	user, project := t.TempDir(), t.TempDir()
	useWorkflowRoots(t, user, project)

	if got := UserWorkflowsRoot(); got != user {
		t.Errorf("UserWorkflowsRoot() = %q, want %q", got, user)
	}
	if got := ProjectWorkflowsRoot("/whatever/cwd"); got != project {
		t.Errorf("ProjectWorkflowsRoot() = %q, want %q", got, project)
	}
}
