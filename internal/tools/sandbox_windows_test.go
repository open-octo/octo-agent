//go:build windows

package tools

import (
	"context"
	"strings"
	"testing"
)

// TestShellCommand_WindowsWrapsRemoveItem verifies the Windows branch injects
// the Remove-Item → trash wrapper and sets the project env, so agent-issued
// deletes are recoverable (parity with the POSIX rm wrapper).
func TestShellCommand_WindowsWrapsRemoveItem(t *testing.T) {
	cmd, err := shellCommand(context.Background(), "Remove-Item foo")
	if err != nil {
		t.Fatalf("shellCommand: %v", err)
	}
	joined := strings.Join(cmd.Args, "\n")
	if !strings.Contains(joined, "function Remove-Item") {
		t.Errorf("windows shellCommand should inject the Remove-Item trash wrapper; args=%v", cmd.Args)
	}
	if !strings.Contains(joined, "__trash-backup") {
		t.Errorf("wrapper should call `octo __trash-backup`; args=%v", cmd.Args)
	}
	var hasProj bool
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "OCTO_TRASH_PROJECT=") {
			hasProj = true
		}
	}
	if !hasProj {
		t.Error("OCTO_TRASH_PROJECT should be set for the trash wrapper")
	}
}
