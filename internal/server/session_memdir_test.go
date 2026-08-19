package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/memory"
)

// A session filed under a project writes into that project's memory, and the
// server's launch directory has no say in it — a serve/desktop process started
// from home used to file every session's notes under the global home slug.
func TestSessionMemDir_ScopedToProject(t *testing.T) {
	homeMem, err := memory.HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cwd: t.TempDir(), homeMemDir: homeMem}

	// A plain directory, deliberately not a git repo: being a project is what
	// earns project memory now, not being a checkout.
	projectDir := t.TempDir()

	got := s.sessionMemDir(projectDir)
	want, err := memory.Dir(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("sessionMemDir(%q) = %q, want the project's dir %q", projectDir, got, want)
	}
	if got == homeMem {
		t.Error("a project session must not fall back to the shared tier")
	}
	if fi, err := os.Stat(got); err != nil || !fi.IsDir() {
		t.Errorf("sessionMemDir must create the directory; stat: %v", err)
	}
	home, _ := os.UserHomeDir()
	if !strings.HasPrefix(got, filepath.Join(home, ".octo", "memories")+string(filepath.Separator)) {
		t.Errorf("dir %q not under ~/.octo/memories", got)
	}

	// Stable: repeated resolution for the same project returns the same dir.
	if again := s.sessionMemDir(projectDir); again != got {
		t.Errorf("second resolution %q != first %q", again, got)
	}
}

// The black-hole case that motivated scoping by project rather than by
// directory: a loose task, pointed at some scratch directory, while the user
// verbally asks for work on a project. Its notes must land in the shared tier
// every session reads, not in a slug directory nothing else opens.
func TestSessionMemDir_TaskUsesSharedTier(t *testing.T) {
	homeMem, err := memory.HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cwd: t.TempDir(), homeMemDir: homeMem}

	if got := s.sessionMemDir(""); got != homeMem {
		t.Errorf("a session in no project = %q, want the shared home dir %q", got, homeMem)
	}
}

// A directory a session merely runs in is not a project. Only what the session
// group registry calls a project reaches sessionMemDir, so the server never
// invents project memory for a working directory on its own — the regression
// that buried notes under a slug for the default workspace.
func TestSessionMemDir_WorkingDirAloneIsNotAProject(t *testing.T) {
	homeMem, err := memory.HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(t.TempDir(), "Octo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{cwd: workspace, homeMemDir: homeMem}

	// The session is in no project, so it passes "" however its own working
	// directory is set — including when that directory is the server's.
	got := s.sessionMemDir(s.sessionProjectDir("no-such-session"))
	if got != homeMem {
		t.Errorf("ungrouped session = %q, want the shared home dir %q", got, homeMem)
	}
	if slug, err := memory.Dir(workspace); err == nil && got == slug {
		t.Errorf("workspace got its own slug dir %q — notes would be invisible to other sessions", slug)
	}
}

func TestSessionMemDir_DisabledByNoMemory(t *testing.T) {
	s := &Server{cwd: t.TempDir(), homeMemDir: "/srv/home-memdir", cfg: Config{NoMemory: true}}
	if got := s.sessionMemDir(t.TempDir()); got != "" {
		t.Errorf("NoMemory must disable per-session memory; got %q", got)
	}
	if got := s.sessionMemDir(""); got != "" {
		t.Errorf("NoMemory must disable the shared tier too; got %q", got)
	}
}
