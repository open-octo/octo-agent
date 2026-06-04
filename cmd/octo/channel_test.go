package main

import (
	"context"
	"testing"
)

// TestNewChannelGate_NonInteractive verifies the IM bridge gate: safe tools are
// allowed, while ask-class tools resolve to deny (there is no chat prompt to
// confirm over). This is the IM path's first permission enforcement — before
// PR3 the channel ran tools ungated.
func TestNewChannelGate_NonInteractive(t *testing.T) {
	gate, err := newChannelGate(t.TempDir())
	if err != nil {
		t.Fatalf("newChannelGate: %v", err)
	}

	ctx := context.Background()

	// A safe, auto-allowed command runs.
	if allow, _ := gate.Check(ctx, "terminal", map[string]any{"command": "ls -la"}); !allow {
		t.Error("expected `ls -la` to be allowed")
	}

	// An ask-class command (sudo) has no allow rule → implicit ask → strict
	// mode + nil asker → deny.
	if allow, reason := gate.Check(ctx, "terminal", map[string]any{"command": "sudo rm /tmp/x"}); allow {
		t.Error("expected `sudo` to be denied in the non-interactive gate")
	} else if reason == "" {
		t.Error("expected a denial reason for the model to see")
	}

	// A hard-deny command is refused regardless of mode.
	if allow, _ := gate.Check(ctx, "terminal", map[string]any{"command": "rm -rf /"}); allow {
		t.Error("expected `rm -rf /` to be denied")
	}
}
