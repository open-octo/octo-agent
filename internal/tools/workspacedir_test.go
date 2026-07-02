package tools

import "testing"

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
