package memory

import (
	"path/filepath"
	"strings"
	"testing"
)

// The slug is derived from the project's stable identity: a readable base
// (the workspace basename, fixed at creation) plus a hash of the project ID.
// Renaming the project touches neither input, so memory never moves.
func TestDirForProjectID_StableAndReadable(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	dir, err := DirForProjectID("g-abc12345", "订单重构")
	if err != nil {
		t.Fatalf("DirForProjectID: %v", err)
	}
	root, err := RootDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dir) != root {
		t.Errorf("dir %q is not directly under the memories root %q", dir, root)
	}
	seg := filepath.Base(dir)
	if !strings.Contains(seg, "-p") {
		t.Errorf("slug %q lacks the -p id-hash marker that separates it from path slugs", seg)
	}

	again, err := DirForProjectID("g-abc12345", "订单重构")
	if err != nil || again != dir {
		t.Errorf("same identity resolved to %q then %q", dir, again)
	}
	other, err := DirForProjectID("g-deadbeef", "订单重构")
	if err != nil || other == dir {
		t.Errorf("two projects sharing a base collided on %q", dir)
	}
}

// A base with no sluggable characters still yields a usable directory.
func TestDirForProjectID_UnusableBase(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	dir, err := DirForProjectID("g-abc12345", "———")
	if err != nil {
		t.Fatalf("DirForProjectID: %v", err)
	}
	if base := filepath.Base(dir); base == "" || strings.HasPrefix(base, "-") {
		t.Errorf("unusable base produced slug %q", base)
	}
}
