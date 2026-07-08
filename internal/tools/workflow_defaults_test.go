package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializeDefaultWorkflows_WritesEmbeddedAndStamps(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workflows-default")

	if err := materializeDefaultWorkflows(root, "v1", false); err != nil {
		t.Fatalf("materializeDefaultWorkflows: %v", err)
	}
	for _, name := range []string{"batch-migrate.rb", "daily-triage.rb", "parallel-understand.rb"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("expected %s materialized: %v", name, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(root, defaultWorkflowStampFile))
	if err != nil || string(b) != "v1" {
		t.Fatalf("stamp = %q, %v; want v1", string(b), err)
	}
}

func TestMaterializeDefaultWorkflows_NoOpWhenCurrent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workflows-default")
	if err := materializeDefaultWorkflows(root, "v1", false); err != nil {
		t.Fatal(err)
	}
	// Drop a sentinel; a no-op (same version) must not wipe the dir.
	sentinel := filepath.Join(root, "sentinel")
	if err := os.WriteFile(sentinel, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := materializeDefaultWorkflows(root, "v1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("same-version call should be a no-op, but the dir was rewritten")
	}

	// A version bump (or force, e.g. UpdateDefaultWorkflows) re-materializes,
	// wiping stale files.
	if err := materializeDefaultWorkflows(root, "v2", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Errorf("version bump should wipe-and-rewrite the default root")
	}
}

func TestMaterializeDefaultWorkflows_ForceRewritesRegardlessOfStamp(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workflows-default")
	if err := materializeDefaultWorkflows(root, "v1", false); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "sentinel")
	if err := os.WriteFile(sentinel, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := materializeDefaultWorkflows(root, "v1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Errorf("force should wipe-and-rewrite even at the same version")
	}
}

func TestMaterializeDefaultWorkflows_EmptyRootIsNoOp(t *testing.T) {
	if err := materializeDefaultWorkflows("", "v1", false); err != nil {
		t.Errorf("empty root should be a no-op, got %v", err)
	}
}

func TestDiscoverWorkflows_DefaultSurfacesAndIsOverridable(t *testing.T) {
	defaultRoot := filepath.Join(t.TempDir(), "workflows-default")
	orig := defaultWorkflowsRoot
	defaultWorkflowsRoot = func() string { return defaultRoot }
	t.Cleanup(func() { defaultWorkflowsRoot = orig })
	if err := materializeDefaultWorkflows(defaultRoot, "v1", false); err != nil {
		t.Fatal(err)
	}

	userRoot := t.TempDir()
	ou, op := userWorkflowsRoot, projectWorkflowsRoot
	userWorkflowsRoot = func() string { return userRoot }
	projectWorkflowsRoot = func(_ string) string { return "" }
	t.Cleanup(func() { userWorkflowsRoot, projectWorkflowsRoot = ou, op })

	// Default-only: batch-migrate is discovered with source "default".
	w, ok := lookupWorkflow(context.Background(), "batch-migrate")
	if !ok {
		t.Fatal("default batch-migrate not discovered")
	}
	if w.source != "default" {
		t.Errorf("source = %q, want default", w.source)
	}

	// A user workflow of the same name overrides the default.
	writeWorkflowFile(t, userRoot, "batch-migrate.rb", "# @description my override\n\"x\"\n")
	w, ok = lookupWorkflow(context.Background(), "batch-migrate")
	if !ok || w.source != "user" || w.description != "my override" {
		t.Errorf("user workflow should override default, got %+v, ok=%v", w, ok)
	}
}
