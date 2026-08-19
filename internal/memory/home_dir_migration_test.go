package memory

import (
	"os"
	"path/filepath"
	"testing"
)

// The shared tier's slug must not move under a user whose home sits behind a
// symlink. Dir normalizes the path it is given and os.UserHomeDir reports $HOME
// unresolved, so the naive computation would point at a fresh empty directory
// and orphan every global note the user had.
func TestHomeDir_KeepsTheLegacySlugWhenItHoldsTheNotes(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("HOME", link)
	t.Setenv("USERPROFILE", link)

	normalized, err := Dir(link)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := legacyHomeDir(link)
	if err != nil {
		t.Fatal(err)
	}
	if legacy == normalized {
		t.Skip("this home path does not normalize to anything different")
	}

	// Fresh install: nothing written anywhere, so the normalized path wins.
	if got, err := HomeDir(); err != nil || got != normalized {
		t.Fatalf("fresh install: HomeDir = %q (err %v), want %q", got, err, normalized)
	}

	// An earlier version's notes live under the legacy slug: keep reading them
	// rather than silently starting the user over with an empty memory.
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, IndexFile), []byte("- a note\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != legacy {
		t.Errorf("HomeDir = %q, want the legacy slug %q that holds the notes", got, legacy)
	}

	// Once the normalized directory holds notes too, it takes over: this shim
	// exists to reach existing memory, not to pin the old layout forever.
	if err := os.MkdirAll(normalized, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(normalized, IndexFile), []byte("- newer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := HomeDir(); err != nil || got != normalized {
		t.Errorf("HomeDir = %q (err %v), want %q once it holds notes", got, err, normalized)
	}
}

// An empty directory left behind by an earlier EnsureDir is not evidence of
// anything: the normalized path must still win.
func TestHomeDir_IgnoresAnEmptyLegacyDir(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("HOME", link)
	t.Setenv("USERPROFILE", link)

	normalized, err := Dir(link)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := legacyHomeDir(link)
	if err != nil {
		t.Fatal(err)
	}
	if legacy == normalized {
		t.Skip("this home path does not normalize to anything different")
	}
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := HomeDir(); err != nil || got != normalized {
		t.Errorf("HomeDir = %q (err %v), want %q", got, err, normalized)
	}
}
