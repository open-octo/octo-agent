package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// twoCheckouts lays out the shape this whole file is about: the same file
// present at two paths, the way a linked worktree mirrors the main checkout.
// It returns (main, worktree) and writes mainBody / treeBody into them.
func twoCheckouts(t *testing.T, mainBody, treeBody string) (string, string) {
	t.Helper()
	root := t.TempDir()
	main := filepath.Join(root, "repo", "internal", "app")
	tree := filepath.Join(root, "repo-wt", "branch", "internal", "app")
	for _, dir := range []string{main, tree} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mainFile := filepath.Join(main, "provider.go")
	treeFile := filepath.Join(tree, "provider.go")
	if err := os.WriteFile(mainFile, []byte(mainBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(treeFile, []byte(treeBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return mainFile, treeFile
}

func TestReadTracker_IdenticalContentAtAnotherPathWritable(t *testing.T) {
	body := "package app\n\nfunc Provider() string { return \"anthropic\" }\n"
	mainFile, treeFile := twoCheckouts(t, body, body)

	rt := NewReadTracker()
	rt.RecordRead(mainFile)

	// Never read under this name, but byte-for-byte what was read in the main
	// checkout — the same file, reached through the worktree.
	if err := rt.CheckWritable(treeFile); err != nil {
		t.Errorf("identical content at another path should be writable: %v", err)
	}
}

func TestReadTracker_DivergedContentAtAnotherPathRefused(t *testing.T) {
	mainFile, treeFile := twoCheckouts(t,
		"package app\n\nfunc Provider() string { return \"anthropic\" }\n",
		"package app\n\nfunc Provider() string { return \"deepseek\"!! }\n")

	rt := NewReadTracker()
	rt.RecordRead(mainFile)

	err := rt.CheckWritable(treeFile)
	if err == nil || !strings.Contains(err.Error(), "not been read") {
		t.Errorf("a worktree copy that diverged must still be refused, got %v", err)
	}
}

// Same size, different bytes — the cheap size prefilter must not be mistaken
// for the match itself.
func TestReadTracker_SameSizeDifferentBytesRefused(t *testing.T) {
	mainFile, treeFile := twoCheckouts(t, "aaaa\n", "bbbb\n")

	rt := NewReadTracker()
	rt.RecordRead(mainFile)

	err := rt.CheckWritable(treeFile)
	if err == nil || !strings.Contains(err.Error(), "not been read") {
		t.Errorf("same-size but different content must be refused, got %v", err)
	}
}

// A candidate that changed since it was read can't lend its identity to
// another path: those bytes were never shown to the agent.
func TestReadTracker_ContentMatchIgnoresStaleCandidate(t *testing.T) {
	root := t.TempDir()
	mainFile := filepath.Join(root, "main.go")
	treeFile := filepath.Join(root, "tree.go")
	if err := os.WriteFile(mainFile, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rt := NewReadTracker()
	rt.RecordRead(mainFile)

	// Out-of-band edit lands the same new content in both places.
	future := time.Now().Add(2 * time.Hour)
	for _, p := range []string{mainFile, treeFile} {
		if err := os.WriteFile(p, []byte("v2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, future, future); err != nil {
			t.Fatal(err)
		}
	}

	err := rt.CheckWritable(treeFile)
	if err == nil || !strings.Contains(err.Error(), "not been read") {
		t.Errorf("a candidate modified since its read must not alias, got %v", err)
	}
}

func TestReadTracker_ContentMatchSkipsOversizeFile(t *testing.T) {
	root := t.TempDir()
	mainFile := filepath.Join(root, "main.bin")
	treeFile := filepath.Join(root, "tree.bin")
	big := make([]byte, maxContentMatchBytes+1)
	for _, p := range []string{mainFile, treeFile} {
		if err := os.WriteFile(p, big, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rt := NewReadTracker()
	rt.RecordRead(mainFile)

	err := rt.CheckWritable(treeFile)
	if err == nil || !strings.Contains(err.Error(), "not been read") {
		t.Errorf("a file past the digest cap must stay unread, got %v", err)
	}
}

// End to end through the registry: read_file in one checkout, edit_file in the
// other — the flow from the bug report.
func TestRegistry_ReadInMainCheckoutThenEditInWorktree(t *testing.T) {
	body := "package app\n\nfunc Provider() string { return \"anthropic\" }\n"
	mainFile, treeFile := twoCheckouts(t, body, body)

	reg := NewDefaultRegistry()
	ctx := context.Background()
	if _, err := reg.Execute(ctx, "read_file", map[string]any{"path": mainFile}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if _, err := reg.Execute(ctx, "edit_file", map[string]any{
		"path": treeFile, "old_string": "anthropic", "new_string": "openai",
	}); err != nil {
		t.Fatalf("edit in the worktree copy should be allowed: %v", err)
	}

	got, err := os.ReadFile(treeFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "openai") {
		t.Errorf("edit did not land, file is %q", got)
	}
}

// WithFreshTracker is what keeps a sub-agent from inheriting reads it never
// made; it must leave the parent's own state alone.
func TestDefaultRegistry_WithFreshTracker(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	parent := NewDefaultRegistry()
	ctx := context.Background()
	if _, err := parent.Execute(ctx, "read_file", map[string]any{"path": p}); err != nil {
		t.Fatalf("read_file: %v", err)
	}

	child, ok := parent.WithFreshTracker().(DefaultRegistry)
	if !ok {
		t.Fatalf("WithFreshTracker returned %T, want DefaultRegistry", parent.WithFreshTracker())
	}
	if _, err := child.Execute(ctx, "edit_file", map[string]any{
		"path": p, "old_string": "package x", "new_string": "package y",
	}); err == nil || !strings.Contains(err.Error(), "not been read") {
		t.Errorf("a fresh tracker must not inherit the parent's reads, got %v", err)
	}

	// The parent still has its read.
	if err := parent.tracker.CheckWritable(p); err != nil {
		t.Errorf("parent's own read state must survive: %v", err)
	}

	// Enforcement stays off for a registry that never had it.
	if got := (DefaultRegistry{}).WithFreshTracker().(DefaultRegistry); got.tracker != nil {
		t.Error("a tracker-less registry must stay tracker-less")
	}
}
