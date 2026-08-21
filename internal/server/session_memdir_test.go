package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/memory"
)

// A session filed under a project writes into that project's memory, keyed on
// the project's ID — not on any path, so nothing that happens to directories
// on disk can move it. The server's launch directory has no say in it.
func TestSessionMemDir_ScopedToProject(t *testing.T) {
	homeMem, err := memory.HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cwd: t.TempDir(), homeMemDir: homeMem}

	proj := &sessionGroup{ID: "g-cafe0001", Name: "订单重构", WorkingDir: filepath.Join(t.TempDir(), "订单重构")}

	got := s.sessionMemDir(proj)
	want, err := memory.DirForProjectID(proj.ID, filepath.Base(proj.WorkingDir))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("sessionMemDir = %q, want the ID-keyed dir %q", got, want)
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

	// Stable: repeated resolution for the same project returns the same dir,
	// and a renamed project (same ID, new display name) keeps it — the slug's
	// readable half comes from the immutable workspace, not the name.
	if again := s.sessionMemDir(proj); again != got {
		t.Errorf("second resolution %q != first %q", again, got)
	}
	renamed := *proj
	renamed.Name = "改名了"
	if after := s.sessionMemDir(&renamed); after != got {
		t.Errorf("rename moved the memory: %q != %q", after, got)
	}
}

// The black-hole case that motivated scoping by project: a loose task, pointed
// at some scratch directory, while the user verbally asks for work on a
// project. Its notes must land in the shared tier every session reads, not in
// a slug directory nothing else opens.
func TestSessionMemDir_TaskUsesSharedTier(t *testing.T) {
	homeMem, err := memory.HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cwd: t.TempDir(), homeMemDir: homeMem}

	if got := s.sessionMemDir(nil); got != homeMem {
		t.Errorf("a session in no project = %q, want the shared home dir %q", got, homeMem)
	}
}

func TestSessionMemDir_DisabledByNoMemory(t *testing.T) {
	s := &Server{cwd: t.TempDir(), homeMemDir: "/srv/home-memdir", cfg: Config{NoMemory: true}}
	if got := s.sessionMemDir(&sessionGroup{ID: "g-1", WorkingDir: t.TempDir()}); got != "" {
		t.Errorf("NoMemory must disable per-session memory; got %q", got)
	}
	if got := s.sessionMemDir(nil); got != "" {
		t.Errorf("NoMemory must disable the shared tier too; got %q", got)
	}
}
