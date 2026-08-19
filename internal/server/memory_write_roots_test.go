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
	s := &Server{cwd: home, homeMemDir: homeMem}

	roots := s.memoryWriteRoots()
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
	// The widening must stop at the memories tree, resist a sibling that merely
	// shares the prefix as a string, resist traversal out of the tree, and stay
	// under the deny tier for secret-shaped paths.
	octo := filepath.Join(home, ".octo")
	for _, tc := range []struct {
		path string
		want permission.Decision
		why  string
	}{
		{filepath.Join(octo, "config.yml"), permission.Ask, "~/.octo itself is not covered"},
		{filepath.Join(octo, "permissions.yml"), permission.Ask, "the permission config is not covered"},
		{filepath.Join(octo, "sessions", "abc.json"), permission.Ask, "session transcripts are not covered"},
		{wantRoot + "-evil/notes.md", permission.Ask, "a sibling sharing the prefix as a string must not match"},
		{filepath.Join(wantRoot, "..", "config.yml"), permission.Ask, "traversal out of the tree must not match"},
		{filepath.Join(wantRoot, "slug", ".env"), permission.Deny, "the deny tier still beats the widened allow"},
		{filepath.Join(wantRoot, "slug", "id_rsa"), permission.Deny, "key-shaped names stay denied"},
	} {
		if got := e.Check("write_file", map[string]any{"path": tc.path}); got != tc.want {
			t.Errorf("write_file %q: got %s, want %s — %s", tc.path, got, tc.want, tc.why)
		}
	}
}

// The guidance in memory.RenderInjection tells the agent to resolve another
// repo's memory dir with `octo memory path`. That must not be gated: it falls
// through to the implicit ask otherwise, which ModeStrict (the unattended and
// IM default) turns into a deny — leaving the agent unable to follow its own
// instructions in exactly the modes nobody is present to approve them.
func TestOctoMemoryCommandNeverGated(t *testing.T) {
	for _, mode := range []permission.Mode{permission.ModeInteractive, permission.ModeStrict} {
		e, err := permission.New("", "/work", mode)
		if err != nil {
			t.Fatal(err)
		}
		for _, cmd := range []string{"octo memory path /repo", "octo memory list"} {
			if got := e.Check("terminal", map[string]any{"command": cmd}); got != permission.Allow {
				t.Errorf("mode %s: terminal %q got %s, want allow", mode, cmd, got)
			}
		}
		// The allow is anchored at command position and must not hand a pass to
		// a chained destructive command.
		if got := e.Check("terminal", map[string]any{"command": "octo memory path /r && rm -rf /"}); got == permission.Allow {
			t.Errorf("mode %s: a chained rm -rf must not ride the octo-memory allow", mode)
		}
	}
}

// --no-memory must not leave a standing write pass into the memories tree.
func TestMemoryWriteRoots_EmptyWhenMemoryDisabled(t *testing.T) {
	s := &Server{cwd: t.TempDir(), cfg: Config{NoMemory: true}}
	if roots := s.memoryWriteRoots(); len(roots) != 0 {
		t.Errorf("roots = %v, want none when memory is disabled", roots)
	}
}
