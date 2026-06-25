package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestFileTools_HonorWorkingDir verifies the worktree-isolation prerequisite:
// a relative path passed to write_file/read_file resolves against
// WorkingDir(ctx), not the process CWD. Without this a worktree-isolated
// sub-agent's file writes would leak into the main checkout.
func TestFileTools_HonorWorkingDir(t *testing.T) {
	dir := t.TempDir()
	ctx := WithWorkingDir(context.Background(), dir)

	if _, err := (WriteFileTool{}).Execute(ctx, "write_file", map[string]any{
		"path": "sub/rel.txt", "content": "hello",
	}); err != nil {
		t.Fatalf("write_file: %v", err)
	}

	// It must land under the working dir, not the process CWD.
	want := filepath.Join(dir, "sub", "rel.txt")
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("expected file at %s: %v", want, err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want hello", string(data))
	}

	// And read_file with the same relative path + ctx reads it back.
	res, err := (ReadFileTool{}).Execute(ctx, "read_file", map[string]any{"path": "sub/rel.txt"})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if res.Text == "" {
		t.Error("read_file returned empty for the working-dir-relative path")
	}
}
