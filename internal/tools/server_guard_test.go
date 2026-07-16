package tools

import (
	"context"
	"fmt"
	"strconv"
	"testing"
)

func TestGuardServerSelfKill_Disabled(t *testing.T) {
	// Guard off (CLI/TUI): even a self-kill command is allowed to build.
	SetServerGuard(false)
	cmd := fmt.Sprintf("kill %d", serverSelfPID)
	if err := guardServerSelfKill(cmd); err != nil {
		t.Fatalf("guard off should permit %q, got %v", cmd, err)
	}
}

func TestGuardServerSelfKill_Blocks(t *testing.T) {
	SetServerGuard(true)
	defer SetServerGuard(false)

	self := strconv.Itoa(serverSelfPID)
	super := strconv.Itoa(serverSuperPID)
	blocked := []string{
		`pkill -f "octo serve"`,
		"pkill octo",
		"killall octo",
		"kill " + self,
		"kill -9 " + self,
		"kill -TERM " + super,
		"kill $(pgrep octo)",
		"kill $(pidof octo)",
	}
	for _, c := range blocked {
		if err := guardServerSelfKill(c); err == nil {
			t.Errorf("want %q blocked, got nil", c)
		}
	}
}

func TestGuardServerSelfKill_Allows(t *testing.T) {
	SetServerGuard(true)
	defer SetServerGuard(false)

	allowed := []string{
		"ls -la",
		"git status",
		"kill 999999999",          // some unrelated pid, not ours
		"pkill -f my-test-server", // not octo
		"killall octoprint",       // 'octo' substring, not the octo binary
		"echo skill",              // 'skill' must not trip the \bkill\b rule
		"kill %1",                 // job spec, no pid
		"grep octo README.md",     // mentions octo but no kill
		"systemctl restart myapp", // unrelated
		// A commit message that merely mentions kill with signal / negative
		// process-group arguments must not be mistaken for a self-kill. The
		// "-1" here is not a PID target, and PPID 1 (init) is never protected.
		`git commit -m "deny catastrophe commands (kill -9 -1, rm -rf /)"`,
		"kill -9 -1", // signal-all/PGID form, no bare target PID
	}
	for _, c := range allowed {
		if err := guardServerSelfKill(c); err != nil {
			t.Errorf("want %q allowed, got %v", c, err)
		}
	}
}

// TestGuardServerSelfKill_PPID1NotProtected guards against the false positive
// where a server reparented to init/launchd (PPID 1) blocked any command whose
// text contained the digit "1". With PPID 1 excluded, only the real self PID is
// protected.
func TestGuardServerSelfKill_PPID1NotProtected(t *testing.T) {
	SetServerGuard(true)
	defer SetServerGuard(false)

	orig := serverSuperPID
	serverSuperPID = 1
	defer func() { serverSuperPID = orig }()

	if err := guardServerSelfKill("kill 1"); err != nil {
		t.Errorf("PPID 1 must not be protected, got %v", err)
	}
	// The real self PID is still blocked regardless of the parent.
	if err := guardServerSelfKill("kill " + strconv.Itoa(serverSelfPID)); err == nil {
		t.Error("self PID must still be blocked when PPID is 1")
	}
}

// TestGuardServerSelfKill_ViaShellCommand confirms the guard fires at the
// single command-execution chokepoint, so terminal / background / detached all
// inherit it.
func TestGuardServerSelfKill_ViaShellCommand(t *testing.T) {
	SetServerGuard(true)
	defer SetServerGuard(false)

	if _, err := shellCommand(context.Background(), "pkill octo"); err == nil {
		t.Fatal("shellCommand should refuse to build a self-kill command")
	}
	if _, err := shellCommand(context.Background(), "echo hi"); err != nil {
		t.Fatalf("shellCommand should build a benign command, got %v", err)
	}
}
