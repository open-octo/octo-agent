//go:build darwin || linux

package server

import (
	"context"
	"testing"
	"time"

	"github.com/Leihb/octo-agent/internal/tools"
)

// TestShutdown_KillsBackgroundProcesses guards the daemon-shutdown orphan fix:
// background commands started through web/IM sessions run in the process-global
// manager, and Server.Shutdown must reap them so they don't outlive the server.
func TestShutdown_KillsBackgroundProcesses(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	// Launch a detached process in the same process-global manager that the
	// web/IM terminal tool uses.
	term := tools.TerminalTool{}
	if _, err := term.Execute(context.Background(), "terminal", map[string]any{
		"command":           "sleep 60",
		"run_in_background": "async",
	}); err != nil {
		t.Fatalf("start background: %v", err)
	}
	if len(tools.RunningBackground()) == 0 {
		t.Fatal("expected a running background process after launch")
	}

	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Shutdown must take the background process down; poll until none remain.
	for i := 0; i < 100; i++ {
		if len(tools.RunningBackground()) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("background process survived server Shutdown")
}
