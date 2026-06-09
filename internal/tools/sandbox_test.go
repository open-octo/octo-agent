package tools

import (
	"context"
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
