package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/agentprofile"
)

// TestRunChat_UnknownAgentErrors verifies that --agent with an unknown ID
// prints an error and exits 2 (without needing an API key).
func TestRunChat_UnknownAgentErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var stdout, stderr bytes.Buffer
	code := run([]string{"--agent", "nonexistent", "hello"}, os.Stdin, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2 for unknown agent, got %d", code)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("not found")) {
		t.Fatalf("expected 'not found' error, got: %s", stderr.String())
	}
}

// TestRunChat_AgentFlagListsAvailableOnError verifies that the error message
// for an unknown --agent value lists the available profiles.
func TestRunChat_AgentFlagListsAvailableOnError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Create a profile so we can verify it appears in the error listing.
	dir := filepath.Join(home, ".octo", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	store := agentprofile.New(dir, func() string { return "" })
	if err := store.Create(&agentprofile.Profile{
		ID:             "my-agent",
		Name:           "My Agent",
		Description:    "test",
		CapabilitySpec: agentprofile.CapabilitySpec{Model: "claude-sonnet-4-20250514"},
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"--agent", "nonexistent", "hello"}, os.Stdin, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2 for unknown agent, got %d", code)
	}
	// Error should mention the created profile as available.
	if !bytes.Contains(stderr.Bytes(), []byte("my-agent")) {
		t.Fatalf("expected error to list available profiles, got: %s", stderr.String())
	}
	// When no user profiles exist, the listing should not end with an orphan ", ".
	if !bytes.Contains(stderr.Bytes(), []byte("available: default")) {
		t.Fatalf("error should list 'default' in available profiles, got: %s", stderr.String())
	}
}

// TestRunChat_AgentFlagResolvesByID verifies that --agent with a valid profile
// ID advances past agent resolution (exits with a different code than 2,
// typically an API key error in test environments). Name-based lookup is not
// yet implemented (store.Get is ID-only).
func TestRunChat_AgentFlagResolvesByID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	dir := filepath.Join(home, ".octo", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	store := agentprofile.New(dir, func() string { return "" })
	if err := store.Create(&agentprofile.Profile{
		ID:             "my-agent",
		Name:           "My Agent",
		Description:    "test",
		CapabilitySpec: agentprofile.CapabilitySpec{Model: "claude-sonnet-4-20250514"},
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"--agent", "my-agent", "hello"}, os.Stdin, &stdout, &stderr)
	if code == 2 {
		// Exit 2 is a usage error (e.g. agent not found). A valid agent
		// should get past resolution; any later failure (e.g. API key) is
		// a different code.
		t.Fatalf("valid agent should not exit with usage error (2), got: %s", stderr.String())
	}
}
