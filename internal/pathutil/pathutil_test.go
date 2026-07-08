package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSameDir_IdenticalPaths(t *testing.T) {
	dir := t.TempDir()
	if !SameDir(dir, dir) {
		t.Errorf("SameDir(%q, %q) = false, want true", dir, dir)
	}
}

func TestSameDir_DifferentPaths(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if SameDir(a, b) {
		t.Errorf("SameDir(%q, %q) = true, want false", a, b)
	}
}

func TestSameDir_Empty(t *testing.T) {
	if SameDir("", "") {
		t.Error("SameDir(\"\", \"\") = true, want false")
	}
	if SameDir("", t.TempDir()) {
		t.Error("SameDir(\"\", dir) = true, want false")
	}
}

// A symlinked $HOME reached two different ways (once resolved, once not)
// must still compare equal — this is the scenario that let a home-cwd
// collision slip through a raw string comparison (os.Getwd()'s syscall
// fallback returns a symlink-resolved path while os.UserHomeDir() returns
// $HOME verbatim).
func TestSameDir_ThroughSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require elevated privileges on windows")
	}
	real := t.TempDir()
	root := t.TempDir()
	link := filepath.Join(root, "home-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	unresolved := filepath.Join(link, ".octo", "skills")
	resolved := filepath.Join(real, ".octo", "skills")
	if !SameDir(unresolved, resolved) {
		t.Errorf("SameDir(%q, %q) = false, want true (same dir via symlink)", unresolved, resolved)
	}
}

// The trailing subpath (e.g. ".octo/mcp.json") need not exist for the
// comparison to still resolve the existing ancestor correctly.
func TestSameDir_NonexistentSuffix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require elevated privileges on windows")
	}
	real := t.TempDir()
	root := t.TempDir()
	link := filepath.Join(root, "home-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	unresolved := filepath.Join(link, ".octo", "mcp.json")
	resolved := filepath.Join(real, ".octo", "mcp.json")
	if !SameDir(unresolved, resolved) {
		t.Errorf("SameDir(%q, %q) = false, want true (neither file exists yet)", unresolved, resolved)
	}
}
