package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/memory"
	"github.com/open-octo/octo-agent/internal/permission"
)

// The permission engine must whitelist the whole memories tree, so a session
// can file a durable fact into ANOTHER project's memory dir without a prompt.
// Before this, only the session's own dir + home were writable, which made the
// cross-project case (a session with no project of its own asked to work on
// one) prompt on every save.
func TestMemoryWriteRoots_CoversOtherProjectsDirs(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	homeMem, err := memory.HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cwd: home, memDir: homeMem, homeMemDir: homeMem}

	roots := s.memoryWriteRoots(home)
	if len(roots) != 1 {
		t.Fatalf("roots = %v, want exactly the memories root", roots)
	}
	wantRoot, err := memory.RootDir()
	if err != nil {
		t.Fatal(err)
	}
	if roots[0] != wantRoot {
		t.Errorf("root = %q, want %q", roots[0], wantRoot)
	}

	e, err := permission.New("", "/work", permission.ModeInteractive, roots...)
	if err != nil {
		t.Fatal(err)
	}
	// Another project's memory dir — the whole point of the widening.
	other := filepath.Join(wantRoot, "someotherrepo-0badf00d", "MEMORY.md")
	if got := e.Check("write_file", map[string]any{"path": other}); got != permission.Allow {
		t.Errorf("write_file %q: got %s, want allow", other, got)
	}
	if got := e.Check("edit_file", map[string]any{"path": other}); got != permission.Allow {
		t.Errorf("edit_file %q: got %s, want allow", other, got)
	}
	// The widening stops at the memories tree: ~/.octo itself is not writable.
	outside := filepath.Join(home, ".octo", "config.yml")
	if got := e.Check("write_file", map[string]any{"path": outside}); got == permission.Allow {
		t.Errorf("write_file %q: got allow, want ask/deny — the root must not cover ~/.octo", outside)
	}
}

// --no-memory must not leave a standing write pass into the memories tree.
func TestMemoryWriteRoots_EmptyWhenMemoryDisabled(t *testing.T) {
	s := &Server{cwd: t.TempDir(), cfg: Config{NoMemory: true}}
	if roots := s.memoryWriteRoots(s.cwd); len(roots) != 0 {
		t.Errorf("roots = %v, want none when memory is disabled", roots)
	}
}
