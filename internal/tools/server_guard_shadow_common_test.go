package tools

import (
	"context"
	"testing"
)

// runGuardedCommand builds and runs a terminal command through shellCommand
// with whatever guard state the caller has set up, returning the combined
// output and the process error (non-nil on refusal as well as on ordinary
// command failure). Shared by the POSIX and Windows shadow tests.
func runGuardedCommand(t *testing.T, command string) (string, error) {
	t.Helper()
	cmd, err := shellCommand(context.Background(), command)
	if err != nil {
		t.Fatalf("shellCommand(%q) failed to build: %v", command, err)
	}
	out, runErr := cmd.CombinedOutput()
	return string(out), runErr
}
