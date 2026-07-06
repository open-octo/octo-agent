package tools

import (
	"path/filepath"
	"testing"
)

// Unset config: no override, today's behavior (server launch dir) is untouched.
func TestResolveWorkspaceDir_Empty(t *testing.T) {
	got, err := ResolveWorkspaceDir("")
	if err != nil {
		t.Fatalf("ResolveWorkspaceDir(\"\") error = %v, want nil", err)
	}
	if got != "" {
		t.Fatalf("ResolveWorkspaceDir(\"\") = %q, want \"\"", got)
	}
}

// A literal path (anything other than "" or "auto") is a power-user override:
// returned unchanged.
func TestResolveWorkspaceDir_LiteralPath(t *testing.T) {
	const want = "/some/literal/path"
	got, err := ResolveWorkspaceDir(want)
	if err != nil {
		t.Fatalf("ResolveWorkspaceDir(%q) error = %v, want nil", want, err)
	}
	if got != want {
		t.Fatalf("ResolveWorkspaceDir(%q) = %q, want %q", want, got, want)
	}
}

// A leading "~" in a literal path is expanded to the user's home directory,
// matching the "~/code/my-project" example shown in the Settings UI.
func TestResolveWorkspaceDir_TildeExpansion(t *testing.T) {
	home := setTestHomeDir(t)

	got, err := ResolveWorkspaceDir("~/code/my-project")
	if err != nil {
		t.Fatalf("ResolveWorkspaceDir(\"~/code/my-project\") error = %v, want nil", err)
	}
	want := filepath.Join(home, "code", "my-project")
	if got != want {
		t.Fatalf("ResolveWorkspaceDir(\"~/code/my-project\") = %q, want %q", got, want)
	}
}

func setTestHomeDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	return tmp
}

// "auto" resolves to ~/Desktop/octo — a discoverable, non-technical-user
// friendly default. No existence check: Desktop (and octo under it) is
// created lazily via MkdirAll the first time a session actually needs it.
func TestResolveWorkspaceDir_Auto(t *testing.T) {
	home := setTestHomeDir(t)

	got, err := ResolveWorkspaceDir("auto")
	if err != nil {
		t.Fatalf("ResolveWorkspaceDir(\"auto\") error = %v, want nil", err)
	}
	want := filepath.Join(home, "Desktop", "octo")
	if got != want {
		t.Fatalf("ResolveWorkspaceDir(\"auto\") = %q, want %q", got, want)
	}
}
