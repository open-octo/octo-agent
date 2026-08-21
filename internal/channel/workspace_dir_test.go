package channel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// A chat message carries no directory the user is standing in, so an IM session
// used to have none — and its tools fell back to wherever `octo serve` was
// launched from, which is nobody's choice and changes with how the server was
// started. These pin that every way of creating an IM session records the
// configured workspace instead, as a real property of the session so that every
// surface reading it (web, IM, the CLI's listing) gets the same answer.

func TestNewChannelStore_RecordsTheWorkspace(t *testing.T) {
	tempHome(t)
	want, err := tools.ResolveWorkspaceDir("")
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}

	st := newChannelStore("im-test-1", "stub-model", "")
	if st.WorkingDir != want {
		t.Errorf("WorkingDir = %q, want the workspace %q", st.WorkingDir, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("the workspace a session is about to work in was not created: %v", err)
	}
}

// The recorded directory has to survive to disk — the server reads it back via
// LoadSession, so an in-memory-only value would leave the resolution where it
// was.
func TestNewChannelStore_WorkspacePersists(t *testing.T) {
	tempHome(t)
	want, err := tools.ResolveWorkspaceDir("")
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}

	st := newChannelStore("im-test-2", "stub-model", "")
	if err := st.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := agent.LoadSession("im-test-2")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.WorkingDir != want {
		t.Errorf("persisted WorkingDir = %q, want %q", loaded.WorkingDir, want)
	}
}

// A configured workspace_dir is honoured, not just the built-in default.
func TestNewChannelStore_HonoursConfiguredWorkspace(t *testing.T) {
	tempHome(t)
	custom := filepath.Join(t.TempDir(), "im-workspace")
	writeWorkspaceConfig(t, custom)

	st := newChannelStore("im-test-3", "stub-model", "")
	if st.WorkingDir != custom {
		t.Errorf("WorkingDir = %q, want the configured workspace %q", st.WorkingDir, custom)
	}
}

// /new must not produce a session shaped differently from the one the restore
// path builds — the two used to diverge only in ways nobody noticed.
func TestCmdNew_RecordsTheWorkspace(t *testing.T) {
	tempHome(t)
	want, err := tools.ResolveWorkspaceDir("")
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}

	m := testManager()
	reply := m.cmdNew(InboundEvent{ChatID: "c1", UserID: "u1"}, "")
	if reply == "" {
		t.Fatal("/new returned no reply")
	}

	sessions, err := agent.ListSessions(0)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatalf("/new created no session (reply: %s)", reply)
	}
	for _, s := range sessions {
		if s.WorkingDir != want {
			t.Errorf("session %s from /new: WorkingDir = %q, want %q", s.ID, s.WorkingDir, want)
		}
	}
}

// writeWorkspaceConfig points ~/.octo/config.yml at dir.
func writeWorkspaceConfig(t *testing.T, dir string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	octo := filepath.Join(home, ".octo")
	if err := os.MkdirAll(octo, 0o700); err != nil {
		t.Fatalf("mkdir ~/.octo: %v", err)
	}
	body := "workspace_dir: " + dir + "\n"
	if err := os.WriteFile(filepath.Join(octo, "config.yml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
