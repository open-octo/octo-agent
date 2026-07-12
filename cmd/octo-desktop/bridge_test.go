package main

import (
	"strings"
	"testing"
)

// TestShellURL pins the shell-marker query the frontend keys nativeShell off.
// The string is duplicated across the Go/JS boundary (see desktopShellQuery and
// web/src/components/layout/VersionBadge.svelte); renaming one side without the
// other silently downgrades the desktop shell to a plain-web client — the OS
// file dialog and native header quietly stop working. This test locks the Go
// side of that contract.
func TestShellURL(t *testing.T) {
	const base = "http://127.0.0.1:8088"

	tests := []struct {
		name string
		hash string
		want string
	}{
		{"no hash", "", base + "/?shell=octo-desktop"},
		{"with hash", "settings", base + "/?shell=octo-desktop#settings"},
		{"nested route", "chat/abc123", base + "/?shell=octo-desktop#chat/abc123"},
	}
	for _, tt := range tests {
		if got := shellURL(base, tt.hash); got != tt.want {
			t.Errorf("shellURL(%q, %q) = %q, want %q", base, tt.hash, got, tt.want)
		}
	}

	// The marker must survive on every variant — it is the sole nativeShell signal.
	if got := shellURL(base, "x"); !strings.Contains(got, "shell=octo-desktop") {
		t.Fatalf("shellURL dropped the desktop marker: %q", got)
	}
}
