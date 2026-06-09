//go:build darwin || linux

package tools

import (
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestKillAll_KillsOrphanedChildren is a regression test for background
// processes surviving session shutdown (TUI exit). The shell wrapper
// backgrounds a child (sleep) and prints its PID; KillAll must take the whole
// process group down, not just SIGKILL the wrapper and leave the child
// reparented to init and alive.
func TestKillAll_KillsOrphanedChildren(t *testing.T) {
	mgr := NewBackgroundManager()

	// `sleep 60 &` makes sleep a background child of the wrapping shell; the
	// shell echoes its PID ($!) and waits. Cancelling the context SIGKILLs only
	// the shell — without a process-group kill, sleep is orphaned and lives on.
	id, err := mgr.Start("sleep 60 & echo $! ; wait")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Read the child PID the shell echoed.
	var childPID int
	for i := 0; i < 50 && childPID == 0; i++ {
		out, _, _, _ := mgr.Read(id)
		if fields := strings.Fields(strings.TrimSpace(out)); len(fields) > 0 {
			if pid, perr := strconv.Atoi(fields[0]); perr == nil {
				childPID = pid
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("did not capture background child PID from shell output")
	}

	// Sanity: the child is alive before shutdown (signal 0 = existence check).
	if err := syscall.Kill(childPID, 0); err != nil {
		t.Fatalf("child %d should be alive before KillAll: %v", childPID, err)
	}

	mgr.KillAll()

	// The child must be reaped along with its process group.
	alive := true
	for i := 0; i < 100; i++ {
		if err := syscall.Kill(childPID, 0); err == syscall.ESRCH {
			alive = false
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if alive {
		_ = syscall.Kill(childPID, syscall.SIGKILL) // don't leak on failure
		t.Fatalf("background child %d survived KillAll (orphaned)", childPID)
	}
}
