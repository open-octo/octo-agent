//go:build darwin || linux

package tools

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

var pidRe = regexp.MustCompile(`pid (\d+)`)

// TestTerminal_Detached_OutlivesHarness is the core guarantee of detached:true —
// the process runs in its own session, is NOT tracked by the BackgroundManager,
// and survives KillAllBackground (the on-exit reap). It also checks output lands
// in the requested log file.
func TestTerminal_Detached_OutlivesHarness(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "daemon.log")

	res, err := TerminalTool{}.Execute(context.Background(), "terminal", map[string]any{
		"command":  "echo hello-daemon; sleep 60",
		"detached": true,
		"log_file": logPath,
	})
	if err != nil {
		t.Fatalf("detached launch: %v", err)
	}

	m := pidRe.FindStringSubmatch(res.Text)
	if m == nil {
		t.Fatalf("no pid in result: %q", res.Text)
	}
	pid, _ := strconv.Atoi(m[1])
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }() // don't leak the sleep

	// Alive right after launch.
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("detached pid %d not alive after launch: %v", pid, err)
	}

	// Not tracked: fire-and-forget means it never enters the manager.
	for _, p := range RunningBackground() {
		t.Fatalf("detached process should be untracked, but RunningBackground lists %+v", p)
	}

	// The whole point: the harness's on-exit reap must NOT touch it.
	KillAllBackground()
	time.Sleep(200 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("detached pid %d was killed by KillAllBackground (should survive): %v", pid, err)
	}

	// Output went to the log file.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(logPath); strings.Contains(string(b), "hello-daemon") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("log file %s never received the daemon's output", logPath)
}

// TestTerminal_Detached_DefaultLogIsNohupOut verifies the nohup-style default
// log path when log_file is omitted.
func TestTerminal_Detached_DefaultLogIsNohupOut(t *testing.T) {
	tmp := t.TempDir()
	ctx := WithWorkingDir(context.Background(), tmp)

	res, err := TerminalTool{}.Execute(ctx, "terminal", map[string]any{
		"command":  "echo via-default; sleep 60",
		"detached": true,
	})
	if err != nil {
		t.Fatalf("detached launch: %v", err)
	}
	m := pidRe.FindStringSubmatch(res.Text)
	if m == nil {
		t.Fatalf("no pid in result: %q", res.Text)
	}
	pid, _ := strconv.Atoi(m[1])
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()

	want := filepath.Join(tmp, "nohup.out")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(want); strings.Contains(string(b), "via-default") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("default log %s never received output", want)
}
