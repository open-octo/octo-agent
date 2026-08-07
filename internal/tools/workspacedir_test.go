package tools

import (
	"path/filepath"
	"testing"
)

// Unset config resolves to the global default, ~/Octo.
func TestResolveWorkspaceDir_Empty(t *testing.T) {
	home := setTestHomeDir(t)

	got, err := ResolveWorkspaceDir("")
	if err != nil {
		t.Fatalf("ResolveWorkspaceDir(\"\") error = %v, want nil", err)
	}
	want := filepath.Join(home, "Octo")
	if got != want {
		t.Fatalf("ResolveWorkspaceDir(\"\") = %q, want %q", got, want)
	}
}

// A literal path (anything other than "") is an explicit override that
// replaces the ~/Octo default: returned unchanged.
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

// The legacy "auto" keyword resolves to the same default as "". #1986 made it
// a literal relative path, but the Windows/macOS installers had been seeding
// `workspace_dir: auto` into every fresh config since before that change —
// those configs would otherwise root every new session at the literal
// directory "auto" under the server's process cwd.
func TestResolveWorkspaceDir_LegacyAutoResolvesToDefault(t *testing.T) {
	home := setTestHomeDir(t)
	want := filepath.Join(home, "Octo")

	for _, raw := range []string{"auto", "Auto", " auto "} {
		got, err := ResolveWorkspaceDir(raw)
		if err != nil {
			t.Fatalf("ResolveWorkspaceDir(%q) error = %v, want nil", raw, err)
		}
		if got != want {
			t.Fatalf("ResolveWorkspaceDir(%q) = %q, want %q", raw, got, want)
		}
	}
}
