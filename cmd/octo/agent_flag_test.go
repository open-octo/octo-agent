package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/agentprofile"
)

// TestRunChat_UnknownAgentErrors verifies that --agent with an unknown ID
// prints an error and exits non-zero (without needing an API key).
func TestRunChat_UnknownAgentErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var stdout, stderr bytes.Buffer
	code := run([]string{"--agent", "nonexistent", "hello"}, os.Stdin, &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected non-zero exit for unknown agent")
	}
	if !bytes.Contains(stderr.Bytes(), []byte("not found")) {
		t.Fatalf("expected 'not found' error, got: %s", stderr.String())
	}
}

// TestRunChat_AgentFlagResolvesByName verifies that --agent can resolve a
// profile by its name (not just ID) and that the error message for unknown
// agents lists available profiles.
func TestRunChat_AgentFlagListsAvailableOnError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Create a profile with a distinct name.
	dir := filepath.Join(home, ".octo", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	store := agentprofile.New(dir, func() string { return "" })
	if err := store.Create(&agentprofile.Profile{
		ID:          "my-agent",
		Name:        "My Agent",
		Description: "test",
		CapabilitySpec: agentprofile.CapabilitySpec{Model: "claude-sonnet-4-20250514"},
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"--agent", "nonexistent", "hello"}, os.Stdin, &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected non-zero exit for unknown agent")
	}
	// Error should mention the created profile as available.
	if !bytes.Contains(stderr.Bytes(), []byte("my-agent")) {
		t.Fatalf("expected error to list available profiles, got: %s", stderr.String())
	}
}
