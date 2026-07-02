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
