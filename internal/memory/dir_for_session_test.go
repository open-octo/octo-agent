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

// A git failure must NOT be read as "not a repo". git exits 128 both outside a
// repo and when it distrusts one (safe.directory dubious ownership), and the
// message is localized — so when git can't answer, the .git entry decides.
// Getting this wrong merges a real project's notes into the shared tier.
func TestDirForSession_GitUnavailableKeepsRepoIsolated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init").CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %v (%s)", err, out)
	}
	homeDir, err := HomeDir()
	if err != nil {
		t.Fatal(err)
	}

	// No git binary reachable at all.
	t.Setenv("PATH", filepath.Join(home, "no-such-bin"))

	dir, inProject, err := DirForSession(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !inProject {
		t.Error("a directory with .git must count as a project even when git cannot be run")
	}
	if dir == homeDir {
		t.Errorf("repo collapsed into the shared tier %q when git was unavailable", homeDir)
	}

	// The rule still works without git for the case it was written for: a
	// non-repo directory shares the global tier.
	scratch := filepath.Join(home, "Octo")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	got, inProject, err := DirForSession(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if inProject || got != homeDir {
		t.Errorf("non-repo without git = %q (inProject=%v), want shared dir %q", got, inProject, homeDir)
	}
}

// A bare repo has no work tree, so it has no project memory of its own — it
// shares the global tier. Pinned because it is a behavior change and it falls
// out of the --git-common-dir fall-through rather than being written directly.
func TestDirForSession_BareRepoUsesSharedTier(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	bare := filepath.Join(t.TempDir(), "repo.git")
	if out, err := exec.Command("git", "init", "--bare", bare).CombinedOutput(); err != nil {
		t.Skipf("git init --bare unavailable: %v (%s)", err, out)
	}

	dir, inProject, err := DirForSession(bare)
	if err != nil {
		t.Fatal(err)
	}
	wantHome, err := HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if inProject || dir != wantHome {
		t.Errorf("bare repo = %q (inProject=%v), want shared dir %q", dir, inProject, wantHome)
	}
}

// A linked worktree shares its main repo's memory — a checkout must not start
// with empty project memory. ProjectRoot has covered this; assert it survives
// through DirForSession too.
func TestDirForSession_LinkedWorktreeSharesRepoDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init").CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %v (%s)", err, out)
	}
	for _, args := range [][]string{
		{"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Skipf("git commit unavailable: %v (%s)", err, out)
		}
	}
	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", wt).CombinedOutput(); err != nil {
		t.Skipf("git worktree add unavailable: %v (%s)", err, out)
	}

	repoDir, _, err := DirForSession(repo)
	if err != nil {
		t.Fatal(err)
	}
	wtDir, inProject, err := DirForSession(wt)
	if err != nil {
		t.Fatal(err)
	}
	if !inProject || wtDir != repoDir {
		t.Errorf("worktree = %q (inProject=%v), want the main repo's dir %q", wtDir, inProject, repoDir)
	}
}

func TestGitEntryAncestor(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	// Nothing anywhere above: must terminate at the filesystem root, not spin.
	if got, ok := gitEntryAncestor(deep); ok {
		t.Errorf("got %q, want no ancestor", got)
	}

	// A .git FILE counts — that is how submodules and linked worktrees mark it.
	marker := filepath.Join(root, "a", ".git")
	if err := os.WriteFile(marker, []byte("gitdir: /elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "a")
	if got, ok := gitEntryAncestor(deep); !ok || got != want {
		t.Errorf("gitEntryAncestor(deep) = %q,%v, want %q,true", got, ok, want)
	}
	// The directory holding .git resolves to itself.
	if got, ok := gitEntryAncestor(want); !ok || got != want {
		t.Errorf("gitEntryAncestor(self) = %q,%v, want %q,true", got, ok, want)
	}
	// A relative path terminates too (filepath.Dir walks down to ".").
	if _, ok := gitEntryAncestor("."); ok {
		t.Log("relative path found a .git above — fine, it terminated")
	}
	// The filesystem root itself must terminate.
	if _, ok := gitEntryAncestor(string(filepath.Separator)); ok {
		t.Log("filesystem root has a .git — unusual but it terminated")
	}
}

// With git unable to answer, every subdirectory of one repo must still resolve
// to a single memory dir — the repo's — rather than a slug each.
func TestDirForSession_GitUnavailableIsStablePerRepo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "internal", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(home, "no-such-bin"))

	rootDir, inProject, err := DirForSession(repo)
	if err != nil {
		t.Fatal(err)
	}
	subDir, _, err := DirForSession(sub)
	if err != nil {
		t.Fatal(err)
	}
	if !inProject || subDir != rootDir {
		t.Errorf("subdir = %q, want the repo's dir %q (inProject=%v)", subDir, rootDir, inProject)
	}
	// And it agrees with what git would have produced, so the same repo keeps
	// one directory whether or not git could be consulted.
	want, err := Dir(resolveSymlinks(repo))
	if err != nil {
		t.Fatal(err)
	}
	if rootDir != want {
		t.Errorf("git-less dir = %q, want the same slug git yields: %q", rootDir, want)
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
