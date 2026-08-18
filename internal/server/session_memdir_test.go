package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/memory"
)

// initRepo makes dir a git repo, skipping the test when git is unavailable.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %v (%s)", err, out)
	}
}

// sessionMemDir must resolve the memory directory from the session's cwd, not
// the server's launch directory — a serve/desktop process launched from home
// would otherwise file every session's memories under the global home slug.
func TestSessionMemDir_PerSessionCwd(t *testing.T) {
	serverCwd := t.TempDir()
	serverDefault, _, err := memory.DirForSession(serverCwd)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cwd: serverCwd, memDir: serverDefault}

	// A real repo is what earns a project-memory directory of its own.
	sessCwd := t.TempDir()
	initRepo(t, sessCwd)

	got := s.sessionMemDir(sessCwd)
	want, err := memory.Dir(memory.ProjectRoot(sessCwd))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("sessionMemDir(%q) = %q, want per-repo dir %q", sessCwd, got, want)
	}
	if got == serverDefault {
		t.Error("a repo session must not share the server default dir")
	}
	if fi, err := os.Stat(got); err != nil || !fi.IsDir() {
		t.Errorf("sessionMemDir must create the directory; stat: %v", err)
	}
	home, _ := os.UserHomeDir()
	if !strings.HasPrefix(got, filepath.Join(home, ".octo", "memories")+string(filepath.Separator)) {
		t.Errorf("dir %q not under ~/.octo/memories", got)
	}

	// Stable: repeated resolution for the same cwd returns the same dir.
	if again := s.sessionMemDir(sessCwd); again != got {
		t.Errorf("second resolution %q != first %q", again, got)
	}
}

// The reported black-hole case: a session with no project, working in the
// default ~/Octo workspace, while the user verbally asks for work on some
// other project. Such a cwd has no project identity, so its notes must land
// in the shared home tier (visible to every session) rather than in a slug
// directory for the scratch dir that nothing else ever reads.
func TestSessionMemDir_NonRepoWorkspaceUsesHomeTier(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	homeMem, err := memory.HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cwd: home, memDir: homeMem, homeMemDir: homeMem}

	workspace := filepath.Join(t.TempDir(), "Octo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	got := s.sessionMemDir(workspace)
	if got != homeMem {
		t.Errorf("sessionMemDir(%q) = %q, want the shared home dir %q", workspace, got, homeMem)
	}
	if slug, err := memory.Dir(workspace); err == nil && got == slug {
		t.Errorf("workspace got its own slug dir %q — notes would be invisible to other sessions", slug)
	}
}

func TestSessionMemDir_ServerCwdAndEmptyUseDefault(t *testing.T) {
	serverCwd := t.TempDir()
	s := &Server{cwd: serverCwd, memDir: "/srv/default-memdir"}

	if got := s.sessionMemDir(serverCwd); got != s.memDir {
		t.Errorf("server-cwd session should reuse the default memDir; got %q", got)
	}
	if got := s.sessionMemDir(""); got != s.memDir {
		t.Errorf("empty cwd should reuse the default memDir; got %q", got)
	}
}

func TestSessionMemDir_DisabledByNoMemory(t *testing.T) {
	s := &Server{cwd: t.TempDir(), memDir: "/srv/default-memdir", cfg: Config{NoMemory: true}}
	if got := s.sessionMemDir(t.TempDir()); got != "" {
		t.Errorf("NoMemory must disable per-session memory; got %q", got)
	}
}
