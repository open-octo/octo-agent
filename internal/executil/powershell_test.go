package executil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// pwshName is the filename LookPath resolves on this platform, so the tests
// that plant a fake pwsh run everywhere rather than being Windows-gated.
func pwshName() string {
	if runtime.GOOS == "windows" {
		return "pwsh.exe"
	}
	return "pwsh"
}

// plantPwsh writes an executable fake pwsh at root's MSI location and returns
// its path.
func plantPwsh(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "PowerShell", "7", "pwsh.exe")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// TestPowerShellIn_ProbesMSIWhenPathMisses is the regression that matters: a
// pwsh installed after this process started is on the machine PATH but not on
// the inherited environment block, so PATH lookup alone leaves the session on
// PowerShell 5.1 for good (the answer is memoized).
func TestPowerShellIn_ProbesMSIWhenPathMisses(t *testing.T) {
	t.Setenv("PATH", "")

	if got := powerShellIn(); got != "powershell" {
		t.Errorf("no pwsh anywhere should fall back to \"powershell\", got %q", got)
	}

	root := t.TempDir()
	want := plantPwsh(t, root)
	if got := powerShellIn(root); got != want {
		t.Errorf("powerShellIn = %q, want the probed MSI path %q", got, want)
	}
}

// TestPowerShellIn_PathWinsOverProbe pins the precedence: a pwsh the user put
// on PATH (a different version, a preview build) must not be displaced by the
// MSI at its fixed location.
func TestPowerShellIn_PathWinsOverProbe(t *testing.T) {
	onPath := t.TempDir()
	chosen := filepath.Join(onPath, pwshName())
	if err := os.WriteFile(chosen, nil, 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PATH", onPath)

	root := t.TempDir()
	plantPwsh(t, root)

	got := powerShellIn(root)
	if got != chosen {
		t.Errorf("powerShellIn = %q, want the PATH pwsh %q", got, chosen)
	}
}

// TestProbePwshMSI covers the probe's own edges. Path-shaped only, so it runs
// on every platform.
func TestProbePwshMSI(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()

	if got := probePwshMSI(first, second); got != "" {
		t.Errorf("no pwsh installed should probe to \"\", got %q", got)
	}
	if got := probePwshMSI("", ""); got != "" {
		t.Errorf("unset Program Files roots should probe to \"\", got %q", got)
	}

	// A relative root is skipped rather than joined, even when it does hold a
	// pwsh: exec.Cmd resolves a relative shell path against the session's
	// working directory, which would run a pwsh.exe out of the user's repo.
	cwd := t.TempDir()
	plantPwsh(t, cwd)
	t.Chdir(cwd)
	if got := probePwshMSI("."); got != "" {
		t.Errorf("a relative root must be skipped, got %q", got)
	}

	// A directory named pwsh.exe must not satisfy the probe.
	if err := os.MkdirAll(filepath.Join(first, "PowerShell", "7", "pwsh.exe"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got := probePwshMSI(first, second); got != "" {
		t.Errorf("a pwsh.exe directory should not count, got %q", got)
	}

	// A later root still wins when the earlier ones hold nothing usable.
	want := plantPwsh(t, second)
	if got := probePwshMSI(first, second); got != want {
		t.Errorf("probe = %q, want %q", got, want)
	}

	// Earlier roots take precedence: ProgramFiles before ProgramFiles(x86).
	earlier := t.TempDir()
	earlierWant := plantPwsh(t, earlier)
	if got := probePwshMSI(earlier, second); got != earlierWant {
		t.Errorf("probe = %q, want the earlier root's %q", got, earlierWant)
	}
}
