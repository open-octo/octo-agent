package tools

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/open-octo/octo-agent/internal/trash"
)

// useWorkflowRoots points the default/user registries at temp dirs for the
// test. The default root is materialized from the real embedded workflow
// scripts (not left empty), so tests exercise the same disk-backed "default"
// tier production uses — deterministically, rather than depending on
// whatever might already be materialized on the machine running the test.
func useWorkflowRoots(t *testing.T, userDir string) {
	t.Helper()
	ou, od := userWorkflowsRoot, defaultWorkflowsRoot
	dflt := t.TempDir()
	if err := materializeDefaultWorkflows(dflt, "test", true); err != nil {
		t.Fatalf("materialize default workflows: %v", err)
	}
	userWorkflowsRoot = func() string { return userDir }
	defaultWorkflowsRoot = func() string { return dflt }
	t.Cleanup(func() { userWorkflowsRoot, defaultWorkflowsRoot = ou, od })
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
	useWorkflowRoots(t, user)
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

func TestWorkflowParams_ParsesRequiredAndOptional(t *testing.T) {
	script := "# @description does a thing\n" +
		"# @param target required: the file to migrate\n" +
		"# @param dry_run: skip to preview, off by default\n" +
		"agent(args[\"target\"])\n"
	got := workflowParams(script)
	if len(got) != 2 {
		t.Fatalf("workflowParams = %+v, want 2 entries", got)
	}
	if got[0].name != "target" || !got[0].required || got[0].description != "the file to migrate" {
		t.Errorf("params[0] = %+v", got[0])
	}
	if got[1].name != "dry_run" || got[1].required || got[1].description != "skip to preview, off by default" {
		t.Errorf("params[1] = %+v, want not required", got[1])
	}
}

// TestWorkflowParams_DescriptionStartingWithRequiredWordIsNotMisparsed guards
// the exact collision a review caught: the colon is what lets a description
// that happens to start with the literal word "required" stay a plain
// optional param, instead of the bare "required" keyword-prefix check
// mistaking it for the required flag and eating the word out of the text.
func TestWorkflowParams_DescriptionStartingWithRequiredWordIsNotMisparsed(t *testing.T) {
	script := "# @param verify: required command to double check\n" + "agent(1)\n"
	got := workflowParams(script)
	if len(got) != 1 {
		t.Fatalf("workflowParams = %+v, want 1 entry", got)
	}
	if got[0].required {
		t.Errorf("params[0].required = true, want false — the word came from the description, not the flag")
	}
	if got[0].description != "required command to double check" {
		t.Errorf("params[0].description = %q, want the full text preserved", got[0].description)
	}
}

// TestWorkflowParams_AtParamPrefixRequiresWordBoundary guards against a
// leading comment like "@parameterized ..." being misread as an @param
// declaration merely because it shares the same leading runes.
func TestWorkflowParams_AtParamPrefixRequiresWordBoundary(t *testing.T) {
	if got := workflowParams("# @parameterized foo bar\nagent(1)\n"); len(got) != 0 {
		t.Errorf("workflowParams = %+v, want none (not a real @param line)", got)
	}
}

func TestWorkflowParams_NoneWhenNoParamComments(t *testing.T) {
	if got := workflowParams("# @description just a note\nagent(\"x\")"); len(got) != 0 {
		t.Errorf("workflowParams = %+v, want none", got)
	}
}

func TestLookupWorkflow_ParsesParams(t *testing.T) {
	user := t.TempDir()
	useWorkflowRoots(t, user)
	writeWorkflowFile(t, user, "migrate.rb",
		"# @description Migrate\n# @param target required: Path to migrate\nagent(args[\"target\"])\n")

	w, ok := lookupWorkflow("migrate")
	if !ok {
		t.Fatal("lookupWorkflow: not found")
	}
	if len(w.params) != 1 || w.params[0].name != "target" || !w.params[0].required {
		t.Errorf("params = %+v", w.params)
	}
}

func TestLookupWorkflow_UnknownName(t *testing.T) {
	useWorkflowRoots(t, t.TempDir())
	if _, ok := lookupWorkflow("nope"); ok {
		t.Error("lookupWorkflow returned ok for unknown name")
	}
}

func TestListWorkflows_SortedUnionOfRoots(t *testing.T) {
	user := t.TempDir()
	useWorkflowRoots(t, user)
	writeWorkflowFile(t, user, "zeta.rb", "\"z\"\n")
	writeWorkflowFile(t, user, "alpha.rb", "\"a\"\n")
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

// TestLookupWorkflow_ParallelUnderstandDeclaresRequiredTarget pins that the
// embedded parallel-understand preset declares its required "target" arg via
// `# @param`, so the workflow tool prompts for it up front instead of silently
// mapping a non-git "this repository" default.
func TestLookupWorkflow_ParallelUnderstandDeclaresRequiredTarget(t *testing.T) {
	useWorkflowRoots(t, "")
	w, ok := lookupWorkflow("parallel-understand")
	if !ok {
		t.Fatal("parallel-understand not found")
	}
	if req := requiredParamNames(w.params); len(req) != 1 || req[0] != "target" {
		t.Errorf("parallel-understand required params = %v, want [target]", req)
	}
}

// TestLookupWorkflow_DailyTriageDeclaresRequiredRepo pins that the
// embedded daily-triage preset declares its required "repo" arg via
// `# @param`, so the workflow tool prompts for it up front instead of
// silently triaging the current directory.
func TestLookupWorkflow_DailyTriageDeclaresRequiredRepo(t *testing.T) {
	useWorkflowRoots(t, "")
	w, ok := lookupWorkflow("daily-triage")
	if !ok {
		t.Fatal("daily-triage not found")
	}
	if req := requiredParamNames(w.params); len(req) != 1 || req[0] != "repo" {
		t.Errorf("daily-triage required params = %v, want [repo]", req)
	}
}

// TestLookupWorkflow_BatchMigrateDeclaresRequiredChange pins that the
// embedded batch-migrate preset declares its required "change" arg via
// `# @param`, so the workflow tool prompts for it up front instead of
// batch-migrate's own defensive empty-string check firing after a wasted run.
func TestLookupWorkflow_BatchMigrateDeclaresRequiredChange(t *testing.T) {
	useWorkflowRoots(t, "")
	w, ok := lookupWorkflow("batch-migrate")
	if !ok {
		t.Fatal("batch-migrate not found")
	}
	if req := requiredParamNames(w.params); len(req) != 1 || req[0] != "change" {
		t.Errorf("batch-migrate required params = %v, want [change]", req)
	}
}

func TestLookupWorkflow_EmbeddedDefaultAlwaysAvailable(t *testing.T) {
	// No user root: every built-in preset must still resolve.
	useWorkflowRoots(t, "")
	for _, name := range []string{"parallel-understand", "batch-migrate", "daily-triage"} {
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
	useWorkflowRoots(t, user)
	writeWorkflowFile(t, user, "batch-migrate.rb", "# @description my override\n\"x\"\n")

	w, ok := lookupWorkflow("batch-migrate")
	if !ok || w.description != "my override" {
		t.Errorf("workflow = %+v, ok = %v; want the user file to override the embedded default", w, ok)
	}
}

func TestGetNamedWorkflow_FoundAndNotFound(t *testing.T) {
	user := t.TempDir()
	useWorkflowRoots(t, user)
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
	useWorkflowRoots(t, "")
	err := DeleteWorkflow("batch-migrate")
	if err == nil {
		t.Fatal("DeleteWorkflow: want error deleting a built-in workflow, got nil")
	}
	if !errors.Is(err, ErrBuiltinWorkflow) {
		t.Errorf("err = %v, want errors.Is(err, ErrBuiltinWorkflow)", err)
	}
}

func TestDeleteWorkflow_UnknownName(t *testing.T) {
	useWorkflowRoots(t, t.TempDir())
	err := DeleteWorkflow("does-not-exist")
	if err == nil {
		t.Fatal("DeleteWorkflow: want error for an unknown name, got nil")
	}
	if !errors.Is(err, ErrWorkflowNotFound) {
		t.Errorf("err = %v, want errors.Is(err, ErrWorkflowNotFound)", err)
	}
}

func TestDeleteWorkflow_RemovesUserFilePermanently(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	user := t.TempDir()
	useWorkflowRoots(t, user)
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

	// Permanent — a workflow the user deleted leaves no shadow copy behind.
	trashed, err := trash.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range trashed {
		if e.Original == path {
			t.Errorf("deleted workflow was staged in the trash: %+v", e)
		}
	}
}

func TestUserWorkflowsRoot_ExportsPrivateResolver(t *testing.T) {
	user := t.TempDir()
	useWorkflowRoots(t, user)

	if got := UserWorkflowsRoot(); got != user {
		t.Errorf("UserWorkflowsRoot() = %q, want %q", got, user)
	}
}
