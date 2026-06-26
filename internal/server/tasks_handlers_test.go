package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/tools"
)

func TestApplyTaskDirectory_RootsTheRun(t *testing.T) {
	dir := t.TempDir()
	a := agent.New(nil, "m")
	a.System = "SYS"
	a.LeanSystem = "LEAN"

	ctx, err := applyTaskDirectory(context.Background(), a, dir)
	if err != nil {
		t.Fatalf("applyTaskDirectory: %v", err)
	}
	// Tools resolve against the dir.
	if got := tools.WorkingDir(ctx); got != dir {
		t.Errorf("WorkingDir(ctx) = %q, want %q", got, dir)
	}
	// Planner / project context use it.
	if a.CWD != dir {
		t.Errorf("a.CWD = %q, want %q", a.CWD, dir)
	}
	// The model is told its cwd in both system-prompt variants, and the
	// original content is preserved.
	want := "Working directory: " + dir
	if !strings.HasPrefix(a.System, want) || !strings.HasSuffix(a.System, "SYS") {
		t.Errorf("a.System = %q", a.System)
	}
	if !strings.HasPrefix(a.LeanSystem, want) || !strings.HasSuffix(a.LeanSystem, "LEAN") {
		t.Errorf("a.LeanSystem = %q", a.LeanSystem)
	}
}

func TestApplyTaskDirectory_EmptyLeanSystemUntouched(t *testing.T) {
	a := agent.New(nil, "m")
	a.System = "SYS"
	// No LeanSystem (e.g. a path that didn't compose one).
	if _, err := applyTaskDirectory(context.Background(), a, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if a.LeanSystem != "" {
		t.Errorf("empty LeanSystem should stay empty, got %q", a.LeanSystem)
	}
}

func TestApplyTaskDirectory_InvalidDirErrors(t *testing.T) {
	a := agent.New(nil, "m")
	a.System = "SYS"

	// Missing directory.
	missing := filepath.Join(t.TempDir(), "nope")
	if _, err := applyTaskDirectory(context.Background(), a, missing); err == nil {
		t.Error("expected an error for a missing directory")
	}

	// A file, not a directory.
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := applyTaskDirectory(context.Background(), a, f); err == nil {
		t.Error("expected an error when the path is a file, not a directory")
	}

	// A failed apply must not have mutated the system prompt.
	if a.System != "SYS" {
		t.Errorf("System mutated on error: %q", a.System)
	}
}
