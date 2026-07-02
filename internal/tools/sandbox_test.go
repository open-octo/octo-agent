package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestShellCommand_PlatformShell verifies shellCommand picks the right shell
// per OS: POSIX `sh -c` on macOS/Linux, PowerShell `-Command` on Windows.
func TestShellCommand_PlatformShell(t *testing.T) {
	cmd, err := shellCommand(context.Background(), "echo hi")
	if err != nil {
		t.Fatalf("shellCommand: %v", err)
	}
	args := cmd.Args
	if runtime.GOOS == "windows" {
		// pwsh/powershell ... -Command "<safe-rm wrapper>\n echo hi" — the
		// command is embedded at the end of the Remove-Item trash wrapper.
		if len(args) < 2 || args[len(args)-2] != "-Command" || !strings.Contains(args[len(args)-1], "echo hi") {
			t.Errorf("windows shell should end with -Command containing \"echo hi\", got %v", args)
		}
		base := strings.ToLower(filepath.Base(args[0]))
		if !strings.Contains(base, "pwsh") && !strings.Contains(base, "powershell") {
			t.Errorf("windows shell should be pwsh/powershell, got %q", args[0])
		}
	} else {
		if len(args) != 3 || args[0] != "sh" || args[1] != "-c" || !strings.Contains(args[2], "echo hi") {
			t.Errorf("posix shell should be [sh -c ...echo hi...], got %v", args)
		}
	}
}

// TestWithBundledBinPath_AppendsAfterExistingSystemPath confirms the bundled
// dir lands at the END of PATH, so a system-installed tool of the same name
// still wins — the bundled copy is a fallback, never a shadow.
func TestWithBundledBinPath_AppendsAfterExistingSystemPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	binDir := filepath.Join(home, ".octo", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}

	systemPath := "/usr/bin" + string(os.PathListSeparator) + "/bin"
	env := withBundledBinPath([]string{"PATH=" + systemPath, "OTHER=1"})

	var pathVal string
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			pathVal = strings.TrimPrefix(kv, "PATH=")
		}
	}
	wantSuffix := string(os.PathListSeparator) + binDir
	if !strings.HasSuffix(pathVal, wantSuffix) {
		t.Errorf("expected PATH to end with %q, got %q", wantSuffix, pathVal)
	}
	if !strings.HasPrefix(pathVal, systemPath) {
		t.Errorf("expected the existing system PATH preserved ahead of the bundled dir; got %q", pathVal)
	}
}

// TestWithBundledBinPath_NoOpWhenBundledDirMissing confirms non-installer
// installs (go install, build-from-source, Linux without a packaged
// installer) get an unmodified env, since ~/.octo/bin never exists for them.
func TestWithBundledBinPath_NoOpWhenBundledDirMissing(t *testing.T) {
	home := t.TempDir() // deliberately no .octo/bin under it
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	in := []string{"PATH=/usr/bin", "OTHER=1"}
	out := withBundledBinPath(in)
	if len(out) != len(in) || out[0] != in[0] || out[1] != in[1] {
		t.Errorf("expected env unchanged when ~/.octo/bin is absent, got %v", out)
	}
}

// TestShellCommand_ResolvesBundledTool is an integration test: it spawns a
// real child process via shellCommand and confirms the child can execute a
// fake tool that exists ONLY under a fake ~/.octo/bin — never on the real
// system PATH — proving the PATH-append actually makes the bundled directory
// resolvable end to end, the way a skill script invoking bundled uv/bun would
// rely on.
func TestShellCommand_ResolvesBundledTool(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	binDir := filepath.Join(home, ".octo", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}

	// A name unlikely to collide with anything genuinely on the host PATH, so
	// success can only be explained by the bundled-dir resolution.
	const marker = "hello-from-octo-bundled-bin"
	var invoke string
	if runtime.GOOS == "windows" {
		script := "@echo off\r\necho " + marker + "\r\n"
		if err := os.WriteFile(filepath.Join(binDir, "octofaketool.cmd"), []byte(script), 0o755); err != nil {
			t.Fatalf("write fake tool: %v", err)
		}
		invoke = "octofaketool" // PowerShell resolves external commands via PATHEXT
	} else {
		script := "#!/bin/sh\necho " + marker + "\n"
		if err := os.WriteFile(filepath.Join(binDir, "octofaketool"), []byte(script), 0o755); err != nil {
			t.Fatalf("write fake tool: %v", err)
		}
		invoke = "octofaketool"
	}

	cmd, err := shellCommand(context.Background(), invoke)
	if err != nil {
		t.Fatalf("shellCommand: %v", err)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running bundled-only tool failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), marker) {
		t.Errorf("expected output %q from bundled tool, got: %s", marker, out)
	}
}
