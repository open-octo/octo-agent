package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceDirForProject_CreatesUnderBase(t *testing.T) {
	base := t.TempDir()
	dir, err := workspaceDirForProject(base, "订单重构")
	if err != nil {
		t.Fatalf("workspaceDirForProject: %v", err)
	}
	if want := filepath.Join(base, "订单重构"); dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("workspace was not created as a directory: %v", err)
	}
}

func TestWorkspaceDirForProject_SuffixOnCollision(t *testing.T) {
	base := t.TempDir()
	first, err := workspaceDirForProject(base, "app")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := workspaceDirForProject(base, "app")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second == first {
		t.Fatalf("second project reused the first workspace %q", first)
	}
	if want := filepath.Join(base, "app-2"); second != want {
		t.Errorf("second = %q, want %q", second, want)
	}
	third, err := workspaceDirForProject(base, "app")
	if err != nil {
		t.Fatalf("third: %v", err)
	}
	if want := filepath.Join(base, "app-3"); third != want {
		t.Errorf("third = %q, want %q", third, want)
	}
}

func TestWorkspaceDirForProject_SanitizesName(t *testing.T) {
	base := t.TempDir()
	dir, err := workspaceDirForProject(base, "a/b:c")
	if err != nil {
		t.Fatalf("sanitized name: %v", err)
	}
	if filepath.Dir(dir) != base {
		t.Errorf("separators survived sanitization: %q", dir)
	}
	if _, err := workspaceDirForProject(base, "///"); err == nil {
		t.Error("a name with no usable characters should be an error, got nil")
	}
	if _, err := workspaceDirForProject("", "ok"); err == nil {
		t.Error("empty base should be an error, got nil")
	}
}
