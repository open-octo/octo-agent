//go:build !windows

package tools

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// runGuardedCommand builds and runs a terminal command through shellCommand
// with the self-kill guard armed, returning the combined output and the
// process error (non-nil on refusal as well as on ordinary command failure).
func runGuardedCommand(t *testing.T, command string) (string, error) {
	t.Helper()
	cmd, err := shellCommand(context.Background(), command)
	if err != nil {
		t.Fatalf("shellCommand(%q) failed to build: %v", command, err)
	}
	out, runErr := cmd.CombinedOutput()
	return string(out), runErr
}

func TestGuardEnv_ArmedOnlyWhenGuardOn(t *testing.T) {
	SetServerGuard(false)
	if got := guardEnv(); len(got) != 0 {
		t.Fatalf("guard off: want no guard env, got %v", got)
	}

	SetServerGuard(true)
	defer SetServerGuard(false)
	env := guardEnv()
	wantPID := "OCTO_SERVER_PID=" + strconv.Itoa(serverSelfPID)
	var hasPID, hasMsg bool
	for _, kv := range env {
		hasPID = hasPID || kv == wantPID
		hasMsg = hasMsg || (strings.HasPrefix(kv, "OCTO_GUARD_MSG=") &&
			strings.Contains(kv, "refusing to kill the octo server process"))
	}
	if !hasPID {
		t.Errorf("guard env missing %q, got %v", wantPID, env)
	}
	if !hasMsg {
		t.Errorf("guard env missing refusal message, got %v", env)
	}
}

// TestKillGuardShadow_BlocksExpandedSelfKill covers the indirections the
// textual guard cannot see: the target pid only appears after the shell
// expands a variable or after pkill resolves a name/pattern at runtime. The
// commands are chosen to slip past guardServerSelfKill so the refusal
// provably comes from the runtime shadow.
func TestKillGuardShadow_BlocksExpandedSelfKill(t *testing.T) {
	SetServerGuard(true)
	defer SetServerGuard(false)

	self := strconv.Itoa(serverSelfPID)
	blocked := []string{
		"P=$(echo " + self + "); kill $P",         // variable indirection
		"P=$(echo " + self + "); kill -s TERM $P", // signal spec consuming an arg
		"P=$(echo " + self + "); kill -n 9 $P",    // -n form
		"pkill -f tools.test",                     // -f pattern without the word "octo"
		"pkill -x tools.test",                     // exact name
		"killall tools.test",                      // killall by name
	}
	for _, c := range blocked {
		// Prove the command actually reaches the runtime shadow: the textual
		// guard must NOT be the one rejecting it.
		if err := guardServerSelfKill(c); err != nil {
			t.Errorf("%q: textual guard rejected it, shadow would go untested: %v", c, err)
			continue
		}
		out, err := runGuardedCommand(t, c)
		if err == nil {
			t.Errorf("%q: want refusal (nonzero exit), command succeeded", c)
			continue
		}
		if !strings.Contains(out, "refusing to kill the octo server process") {
			t.Errorf("%q: want refusal message, got %q (err=%v)", c, out, err)
		}
	}
}

// TestKillGuardShadow_AllowsUnrelatedTargets: the shadow must stay invisible
// for commands that do not target the server — no refusal message, and the
// command's own result (success or the shell's own error) is preserved.
func TestKillGuardShadow_AllowsUnrelatedTargets(t *testing.T) {
	SetServerGuard(true)
	defer SetServerGuard(false)

	allowed := []string{
		"kill 999999999",                         // some unrelated, nonexistent pid
		"pkill -x definitely_no_such_proc_xyz",   // matches nothing
		"killall definitely_no_such_proc_xyz",    // matches nothing
		"git commit -m \"fix pkill regression\"", // kill-word inside an argument
	}
	for _, c := range allowed {
		out, _ := runGuardedCommand(t, c)
		if strings.Contains(out, "refusing to kill the octo server process") {
			t.Errorf("%q: must not be refused, got %q", c, out)
		}
	}

	// A benign command runs through the wrapper untouched.
	out, err := runGuardedCommand(t, "echo hello-guard")
	if err != nil || !strings.Contains(out, "hello-guard") {
		t.Errorf("echo through wrapper: out=%q err=%v", out, err)
	}
}

// TestKillGuardShadow_GuardOffIsInert: with the guard disarmed (plain CLI/TUI
// usage) the shadows are no-ops — no OCTO_SERVER_PID is exported and nothing
// is refused.
func TestKillGuardShadow_GuardOffIsInert(t *testing.T) {
	SetServerGuard(false)

	cmd, err := shellCommand(context.Background(), "echo hi")
	if err != nil {
		t.Fatalf("shellCommand: %v", err)
	}
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "OCTO_SERVER_PID=") || strings.HasPrefix(kv, "OCTO_GUARD_MSG=") {
			t.Errorf("guard off: env must not arm the shadow, found %q", kv)
		}
	}

	out, _ := runGuardedCommand(t, "kill 999999999")
	if strings.Contains(out, "refusing to kill the octo server process") {
		t.Errorf("guard off: nothing may be refused, got %q", out)
	}
}
