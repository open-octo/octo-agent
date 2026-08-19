package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A session that belongs to no project — a loose task — writes into the shared
// home tier, the one every session reads. Filing its notes under a slug of
// their own would bury them where nothing else looks.
func TestDirForProject_TaskUsesHomeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	dir, inProject, err := DirForProject("")
	if err != nil {
		t.Fatal(err)
	}
	if inProject {
		t.Error("a session with no project must not report as being in one")
	}
	want, err := HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != want {
		t.Errorf("DirForProject(%q) = %q, want home dir %q", "", dir, want)
	}
}

// The rule that replaced the git probe: a project directory gets its own
// memory, whether or not git has ever heard of it. Plenty of real work lives in
// directories that are not checkouts.
func TestDirForProject_PlainDirGetsOwnSlug(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Deliberately NOT a git repo — no `git init` anywhere.
	project := t.TempDir()
	if _, err := os.Stat(filepath.Join(project, ".git")); !os.IsNotExist(err) {
		t.Fatalf("fixture must not be a git repo: %v", err)
	}

	dir, inProject, err := DirForProject(project)
	if err != nil {
		t.Fatal(err)
	}
	if !inProject {
		t.Error("a project directory must report as a project, git or no git")
	}
	homeDir, err := HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir == homeDir {
		t.Errorf("project %q fell back to the shared tier %q", project, dir)
	}
	if got := filepath.Dir(dir); got != filepath.Join(home, ".octo", "memories") {
		t.Errorf("memory dir %q is not under the memories root", dir)
	}
}

// Two projects sharing a basename must not share memory.
func TestDirForProject_SameBasenameDistinctDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	parent := t.TempDir()
	a := filepath.Join(parent, "one", "app")
	b := filepath.Join(parent, "two", "app")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	dirA, _, err := DirForProject(a)
	if err != nil {
		t.Fatal(err)
	}
	dirB, _, err := DirForProject(b)
	if err != nil {
		t.Fatal(err)
	}
	if dirA == dirB {
		t.Errorf("two projects named %q collided on %q", "app", dirA)
	}
	// Both still carry the readable basename, so the directories stay
	// recognisable on disk.
	for _, d := range []string{dirA, dirB} {
		if got := filepath.Base(d); !strings.HasPrefix(got, "app-") {
			t.Errorf("slug %q does not start with the basename", got)
		}
	}
}

// One directory reached two ways is one project. On macOS a temp dir is
// reachable as both /var/... and /private/var/..., which would otherwise hash
// to two slugs and split a project's memory in half.
func TestDirForProject_NormalizesSymlinkedPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	viaReal, _, err := DirForProject(real)
	if err != nil {
		t.Fatal(err)
	}
	viaLink, _, err := DirForProject(link)
	if err != nil {
		t.Fatal(err)
	}
	if viaReal != viaLink {
		t.Errorf("same project via symlink got two dirs: %q vs %q", viaReal, viaLink)
	}
}

// Running in the home directory is not a special case in the code, and this
// pins the reason: home's own slug directory IS the shared tier, so a session
// there lands on the global set without any branch for it.
func TestDirForProject_HomeIsTheSharedTier(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	dir, inProject, err := DirForProject(home)
	if err != nil {
		t.Fatal(err)
	}
	if !inProject {
		t.Error("an explicit project dir reports as a project even when it is home")
	}
	want, err := HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != want {
		t.Errorf("DirForProject(home) = %q, want %q", dir, want)
	}
}

// A directory is scoped to itself, not to whatever git would call the repo root
// above it — the whole point of dropping the git probe. Naming a subdirectory
// as the project keeps its memory separate from the parent's.
func TestDirForProject_SubdirIsItsOwnProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	root := t.TempDir()
	sub := filepath.Join(root, "packages", "web")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	rootDir, _, err := DirForProject(root)
	if err != nil {
		t.Fatal(err)
	}
	subDir, _, err := DirForProject(sub)
	if err != nil {
		t.Fatal(err)
	}
	if rootDir == subDir {
		t.Error("a subdirectory named as a project must not share the parent's memory")
	}
}
