package memory

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A working directory that is not a git repo — the default ~/Octo workspace
// being the common case — has no project of its own, so its notes belong in
// the shared home directory. Filing them under a slug for the scratch dir
// would bury them where no other session reads (the ~/Octo black hole).
func TestDirForSession_NonRepoUsesHomeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// A plain directory with no git repo anywhere above it.
	scratch := filepath.Join(home, "Octo")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}

	dir, inProject, err := DirForSession(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if inProject {
		t.Error("a non-repo dir must not report as a project")
	}
	want, err := HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != want {
		t.Errorf("DirForSession(%q) = %q, want home dir %q", scratch, dir, want)
	}
	// The scratch dir must NOT get a slug directory of its own.
	if slug, err := Dir(scratch); err == nil && dir == slug {
		t.Errorf("non-repo dir got its own slug dir %q", slug)
	}
}

func TestDirForSession_RepoGetsOwnSlug(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init").CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %v (%s)", err, out)
	}

	dir, inProject, err := DirForSession(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !inProject {
		t.Error("a git repo must report as a project")
	}
	homeDir, err := HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir == homeDir {
		t.Errorf("a repo must get its own dir, got the home dir %q", dir)
	}
	// ProjectRoot resolves symlinks (macOS /var → /private/var), so compare
	// against the resolved root rather than the raw temp path.
	want, err := Dir(ProjectRoot(repo))
	if err != nil {
		t.Fatal(err)
	}
	if dir != want {
		t.Errorf("DirForSession(%q) = %q, want %q", repo, dir, want)
	}
}

// A subdirectory inside a repo shares the repo's memory, and the home dir
// itself (not a repo) resolves to the shared dir — the two ends of the rule.
func TestDirForSession_SubdirAndHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init").CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %v (%s)", err, out)
	}
	sub := filepath.Join(repo, "internal", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	rootDir, _, err := DirForSession(repo)
	if err != nil {
		t.Fatal(err)
	}
	subDir, inProject, err := DirForSession(sub)
	if err != nil {
		t.Fatal(err)
	}
	if !inProject || subDir != rootDir {
		t.Errorf("subdir resolved to %q, want the repo's dir %q (inProject=%v)", subDir, rootDir, inProject)
	}

	homeSession, inProject, err := DirForSession(home)
	if err != nil {
		t.Fatal(err)
	}
	wantHome, err := HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if inProject || homeSession != wantHome {
		t.Errorf("home cwd = %q (inProject=%v), want shared dir %q", homeSession, inProject, wantHome)
	}
}
