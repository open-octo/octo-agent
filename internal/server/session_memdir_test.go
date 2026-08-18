package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/memory"
)

// sessionMemDir must resolve the memory directory from the session's cwd, not
// the server's launch directory — a serve/desktop process launched from home
// would otherwise file every session's memories under the global home slug.
func TestSessionMemDir_PerSessionCwd(t *testing.T) {
	serverCwd := t.TempDir()
	serverDefault, err := memory.Dir(memory.ProjectRoot(serverCwd))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cwd: serverCwd, memDir: serverDefault}

	sessCwd := t.TempDir()
	got := s.sessionMemDir(sessCwd)
	want, err := memory.Dir(memory.ProjectRoot(sessCwd))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("sessionMemDir(%q) = %q, want per-cwd dir %q", sessCwd, got, want)
	}
	if got == serverDefault {
		t.Error("per-session dir must differ from the server default for a different cwd")
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
