package tools

import (
	"os"
	"testing"

	"github.com/open-octo/octo-agent/internal/sandbox"
)

// TestMain lets the test binary stand in for the `octo` executable when the
// Linux sandbox re-execs itself: sandbox.Command runs os.Executable() with the
// __sandboxed-exec subcommand, and under `go test` that executable IS this test
// binary. Without this dispatch the re-exec would recurse into the test suite.
// On macOS (sandbox-exec, no re-exec) the arg never appears, so this is a no-op.
//
// It also neutralizes the default-workflows root and the workflow journal
// directory for the whole package so tests never touch the real
// ~/.octo/workflows-default or ~/.octo/workflow-journals (which an installed
// binary populates and every workflow run appends to) — mirrors
// internal/skills/defaults_test.go's TestMain. Without the journal redirect,
// every test that runs a workflow (most of workflow_test.go) leaves a .jsonl
// file in the developer's real journal directory forever, since nothing
// prunes it mid-session. Tests that exercise the default-workflows tier opt
// in via useWorkflowRoots, which points defaultWorkflowsRoot at a freshly
// materialized temp dir.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__sandboxed-exec" {
		os.Exit(sandbox.ShimMain())
	}
	defaultsTmp, _ := os.MkdirTemp("", "octo-workflows-default-empty")
	defaultWorkflowsRoot = func() string { return defaultsTmp }
	journalsTmp, _ := os.MkdirTemp("", "octo-workflow-journals-test")
	SetWorkflowJournalDir(journalsTmp)
	code := m.Run()
	for _, tmp := range []string{defaultsTmp, journalsTmp} {
		if tmp != "" {
			_ = os.RemoveAll(tmp)
		}
	}
	os.Exit(code)
}
