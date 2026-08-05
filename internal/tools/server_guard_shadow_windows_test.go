//go:build windows

package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// startGuardDecoy starts a disposable process under a unique image name (a
// copy of cmd.exe running ping) and returns its pid. The Windows shadow tests
// protect the DECOY rather than the test process itself: if the shadow ever
// regresses, the real Stop-Process/taskkill can only hit the decoy — the test
// then fails on the missing refusal message instead of the run killing itself.
func startGuardDecoy(t *testing.T) int {
	t.Helper()
	sysroot := os.Getenv("SystemRoot")
	if sysroot == "" {
		t.Skip("no SystemRoot; cannot stage the decoy binary")
	}
	src, err := os.ReadFile(filepath.Join(sysroot, "System32", "cmd.exe"))
	if err != nil {
		t.Skipf("cannot read cmd.exe to stage the decoy: %v", err)
	}
	exe := filepath.Join(t.TempDir(), "octoguarddecoy.exe")
	if err := os.WriteFile(exe, src, 0o755); err != nil {
		t.Fatalf("staging decoy: %v", err)
	}
	cmd := exec.Command(exe, "/c", "ping", "-n", "120", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting decoy: %v", err)
	}
	// Registered after t.TempDir(), so this runs first (LIFO) and releases the
	// exe file lock before the directory is removed.
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd.Process.Pid
}

// protectPid points the guard at pid instead of the test process, restoring
// the real values on cleanup. The supervisor slot is parked at 1 (never
// protected) so exactly one pid is under guard.
func protectPid(t *testing.T, pid int) {
	t.Helper()
	origSelf, origSuper := serverSelfPID, serverSuperPID
	serverSelfPID, serverSuperPID = pid, 1
	t.Cleanup(func() { serverSelfPID, serverSuperPID = origSelf, origSuper })
}

// TestKillGuardShadowWindows_BlocksResolvedSelfKill is the Windows
// counterpart of TestKillGuardShadow_BlocksExpandedSelfKill: targets that
// only become concrete after PowerShell expands a variable or Get-Process
// resolves a name must be refused by the Stop-Process/taskkill shadows.
// Stop-Process cases carry -WhatIf so a shadow regression performs no kill;
// taskkill has no dry-run, but its only possible victim is the decoy.
func TestKillGuardShadowWindows_BlocksResolvedSelfKill(t *testing.T) {
	decoy := startGuardDecoy(t)
	protectPid(t, decoy)
	SetServerGuard(true)
	defer SetServerGuard(false)

	blocked := []string{
		fmt.Sprintf("Stop-Process -Id %d -WhatIf", decoy),          // literal id
		fmt.Sprintf("$p = %d; Stop-Process -Id $p -WhatIf", decoy), // variable expansion
		fmt.Sprintf("$p = %d; kill -Id $p -WhatIf", decoy),         // kill alias resolves to the shadow
		"Stop-Process -Name octoguarddecoy -WhatIf",                // name resolved via Get-Process
		fmt.Sprintf("taskkill /PID %d", decoy),                     // taskkill function shadow
		fmt.Sprintf("taskkill.exe /PID %d", decoy),                 // extension-qualified spelling (alias)
		"taskkill /F /IM octoguarddecoy.exe",                       // image name resolved via Get-Process
	}
	for _, c := range blocked {
		// The refusal must come from the runtime shadow: the textual guard
		// does not understand PowerShell verbs and must pass these.
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

// TestKillGuardShadowWindows_AllowsUnrelatedTargets: commands that do not
// target a guarded pid pass through the shadows unrefused.
func TestKillGuardShadowWindows_AllowsUnrelatedTargets(t *testing.T) {
	decoy := startGuardDecoy(t)
	protectPid(t, decoy)
	SetServerGuard(true)
	defer SetServerGuard(false)

	allowed := []string{
		"Stop-Process -Id 999999999 -WhatIf",             // unrelated, nonexistent pid
		"Stop-Process -Name no_such_proc_xyz -WhatIf",    // matches nothing
		"taskkill /PID 999999999",                        // unrelated pid; fails on its own terms
		"Write-Output 'taskkill /IM octo.exe regressed'", // kill-words inside a string argument
	}
	for _, c := range allowed {
		out, _ := runGuardedCommand(t, c)
		if strings.Contains(out, "refusing to kill the octo server process") {
			t.Errorf("%q: must not be refused, got %q", c, out)
		}
	}

	out, err := runGuardedCommand(t, "Write-Output hello-guard")
	if err != nil || !strings.Contains(out, "hello-guard") {
		t.Errorf("passthrough through wrapper: out=%q err=%v", out, err)
	}
}

// TestKillGuardShadowWindows_GuardOffIsInert: with the guard disarmed the
// shadows are no-ops, and stale guard variables inherited from a parent
// process are scrubbed rather than arming them.
func TestKillGuardShadowWindows_GuardOffIsInert(t *testing.T) {
	decoy := startGuardDecoy(t)
	SetServerGuard(false)

	t.Setenv("OCTO_SERVER_PID", fmt.Sprint(decoy))
	t.Setenv("OCTO_GUARD_MSG", "stale parent refusal")

	out, _ := runGuardedCommand(t, fmt.Sprintf("Stop-Process -Id %d -WhatIf", decoy))
	if strings.Contains(out, "refusing to kill the octo server process") {
		t.Errorf("guard off: nothing may be refused, got %q", out)
	}
}
