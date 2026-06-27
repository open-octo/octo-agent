package tools

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// waitFor polls fn until it returns true or the deadline passes.
//
// A satisfied check returns within milliseconds, so the deadline only bounds
// the failure case — making it generous costs nothing on the happy path but
// prevents spurious timeouts. Windows needs much more slack: every background
// command pays PowerShell's cold-start cost (seconds, worse under CI load), so
// a tight deadline there turns a perfectly working process into a flake.
func waitFor(t *testing.T, what string, fn func() bool) {
	t.Helper()
	limit := 10 * time.Second
	if runtime.GOOS == "windows" {
		limit = 45 * time.Second
	}
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestBackgroundManager_RunsAndReportsExit(t *testing.T) {
	m := NewBackgroundManager()
	id, err := m.Start("echo hello", BgModeAsync)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	var out, status string
	waitFor(t, "process to exit", func() bool {
		var found bool
		o, s, f, _, _ := m.Read(id)
		found = f
		if o != "" {
			out += o // accumulate across reads (cursor advances)
		}
		status = s
		return found && strings.HasPrefix(s, "exited")
	})

	if !strings.Contains(out, "hello") {
		t.Errorf("output = %q, want it to contain 'hello'", out)
	}
	if status != "exited: 0" {
		t.Errorf("status = %q, want 'exited: 0'", status)
	}
}

func TestBackgroundManager_IncrementalRead(t *testing.T) {
	m := NewBackgroundManager()
	id, err := m.Start("echo one; sleep 0.3; echo two", BgModeAsync)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// First chunk: "one" before "two" is emitted.
	waitFor(t, "first line", func() bool {
		o, _, _, _, _ := m.Read(id)
		return strings.Contains(o, "one")
	})
	// A read right after consuming "one" should not re-return it.
	o, _, _, _, _ := m.Read(id)
	if strings.Contains(o, "one") {
		t.Errorf("second read re-returned old output: %q", o)
	}
	// Eventually "two" arrives as new output.
	waitFor(t, "second line", func() bool {
		o, _, _, _, _ := m.Read(id)
		return strings.Contains(o, "two")
	})
}

func TestBackgroundManager_Kill(t *testing.T) {
	m := NewBackgroundManager()
	id, err := m.Start("sleep 30", BgModeAsync)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !m.Kill(id) {
		t.Fatal("Kill returned false for a live process")
	}
	waitFor(t, "killed process to report exit", func() bool {
		_, s, _, _, _ := m.Read(id)
		return strings.HasPrefix(s, "exited")
	})

	if m.Kill("bg_does_not_exist") {
		t.Error("Kill of unknown id should return false")
	}
}

func TestTerminalTool_BackgroundLaunch(t *testing.T) {
	m := NewBackgroundManager()
	tool := TerminalTool{mgr: m}
	resTool, err := tool.Execute(context.Background(), "terminal", map[string]any{
		"command":           "echo hi",
		"run_in_background": "async",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(resTool.Text, "bg_1") {
		t.Errorf("result = %q, want it to mention the bg id", resTool.Text)
	}
	if !strings.Contains(resTool.Text, "async") {
		t.Errorf("result = %q, want it to mention async mode", resTool.Text)
	}
}

func TestTerminalTool_SyncCommandIsReaped(t *testing.T) {
	// A synchronous command runs as a hidden background process; once it exits
	// and its output is returned, the manager must drop it so its retained
	// buffer doesn't leak for the life of the session.
	m := NewBackgroundManager()
	tool := TerminalTool{mgr: m}

	res, err := tool.Execute(context.Background(), "terminal", map[string]any{
		"command": "echo reaped",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Text, "reaped") {
		t.Errorf("result = %q, want it to contain the output", res.Text)
	}
	// bg_1 was the hidden process — after a clean sync exit it must be gone.
	if _, _, found, _, _ := m.Read("bg_1"); found {
		t.Error("synchronous command's process should have been reaped from the manager")
	}
	if got := m.ListRunning(); len(got) != 0 {
		t.Errorf("ListRunning = %v, want empty after a sync command", got)
	}
}

func TestTerminalOutputTool(t *testing.T) {
	m := NewBackgroundManager()
	term := TerminalTool{mgr: m}
	outTool := TerminalOutputTool{mgr: m}

	if _, err := term.Execute(context.Background(), "terminal", map[string]any{
		"command":           "echo from-bg",
		"run_in_background": "interactive",
	}); err != nil {
		t.Fatalf("launch: %v", err)
	}

	var res string
	waitFor(t, "terminal_output to show exit", func() bool {
		rTool, err := outTool.Execute(context.Background(), "terminal_output", map[string]any{"id": "bg_1"})
		if err != nil {
			t.Fatalf("terminal_output: %v", err)
		}
		res += rTool.Text
		return strings.Contains(res, "exited")
	})
	if !strings.Contains(res, "from-bg") {
		t.Errorf("terminal_output = %q, want it to contain 'from-bg'", res)
	}

	// Unknown id is an error.
	if _, err := outTool.Execute(context.Background(), "terminal_output", map[string]any{"id": "bg_99"}); err == nil {
		t.Error("terminal_output of unknown id should error")
	}
	// Missing id is an error.
	if _, err := outTool.Execute(context.Background(), "terminal_output", map[string]any{}); err == nil {
		t.Error("terminal_output without id should error")
	}
}

func TestKillShellTool(t *testing.T) {
	m := NewBackgroundManager()
	term := TerminalTool{mgr: m}
	killTool := KillShellTool{mgr: m}

	if _, err := term.Execute(context.Background(), "terminal", map[string]any{
		"command":           "sleep 30",
		"run_in_background": "async",
	}); err != nil {
		t.Fatalf("launch: %v", err)
	}
	resKill, err := killTool.Execute(context.Background(), "kill_shell", map[string]any{"id": "bg_1"})
	if err != nil {
		t.Fatalf("kill_shell: %v", err)
	}
	if !strings.Contains(resKill.Text, "killed") {
		t.Errorf("result = %q, want it to note the kill", resKill.Text)
	}

	// Unknown id is an error.
	if _, err := killTool.Execute(context.Background(), "kill_shell", map[string]any{"id": "bg_nope"}); err == nil {
		t.Error("kill_shell on unknown id should error")
	}
}

// TestTerminalTool_TimeoutPromotesToBackground verifies that a synchronous
// command which exceeds TerminalTimeout continues running as the original
// background process — no kill, no restart. The agent receives partial output
// plus the bg id.
func TestTerminalTool_TimeoutPromotesToBackground(t *testing.T) {
	// Use a short timeout so the test doesn't take 30 s.  500 ms is enough
	// for POSIX `sh` and Windows PowerShell to start and emit a line, while
	// keeping the test fast.
	oldTimeout := TerminalTimeout
	TerminalTimeout = 500 * time.Millisecond
	defer func() { TerminalTimeout = oldTimeout }()

	m := NewBackgroundManager()
	term := TerminalTool{mgr: m}

	// Use `echo` (fast, cross-platform) piped to a long sleep.  On POSIX
	// `sleep` is available; on Windows we use `Start-Sleep` via PowerShell.
	cmd := "echo partial && sleep 1"
	if runtime.GOOS == "windows" {
		cmd = "Write-Output partial; Start-Sleep -Seconds 1"
	}
	res, err := term.Execute(context.Background(), "terminal", map[string]any{
		"command": cmd,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Should contain the partial output.
	if !strings.Contains(res.Text, "partial") {
		t.Errorf("result should contain partial output, got: %q", res.Text)
	}
	// Should mention the timeout and that it continues as background.
	if !strings.Contains(res.Text, "timeout") {
		t.Errorf("result should mention timeout, got: %q", res.Text)
	}
	if !strings.Contains(res.Text, "bg_1") {
		t.Errorf("result should mention bg id, got: %q", res.Text)
	}
	// Should contain the async notice warning against polling.
	if !strings.Contains(res.Text, "ASYNC background process") {
		t.Errorf("result should warn against polling, got: %q", res.Text)
	}

	// The background process should eventually finish (sleep 0.5 + margin).
	// Use a longer deadline than the default waitFor because CI under -race
	// can be very slow.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, s, _, _, _ := m.Read("bg_1")
		if strings.HasPrefix(s, "exited") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for background process to exit")
}

// TestTerminalOutputTool_SnapshotIdempotent verifies the snapshot semantics:
// terminal_output is a non-advancing peek, so repeated calls on a running
// process return the current tail and status without error. Empty snapshots are
// still counted for anti-polling, but that is surfaced as extra text in the
// result, not as an error.
func TestTerminalOutputTool_SnapshotIdempotent(t *testing.T) {
	m := NewBackgroundManager()
	term := TerminalTool{mgr: m}
	outTool := TerminalOutputTool{mgr: m}

	if _, err := term.Execute(context.Background(), "terminal", map[string]any{
		"command":           "sleep 30",
		"run_in_background": "interactive",
	}); err != nil {
		t.Fatalf("launch: %v", err)
	}

	for i := 1; i <= 2; i++ {
		res, err := outTool.Execute(context.Background(), "terminal_output", map[string]any{"id": "bg_1"})
		if err != nil {
			t.Fatalf("poll %d should never error in the snapshot model: %v", i, err)
		}
		if !strings.Contains(res.Text, "[status: running]") {
			t.Errorf("poll %d should report running, got %q", i, res.Text)
		}
		if strings.Contains(res.Text, "[STOP:") {
			t.Errorf("poll %d should not be blocked yet, got %q", i, res.Text)
		}
	}
}

// TestTerminalOutputTool_AntiPollBlocksEmptySnapshots verifies that repeated
// empty terminal_output calls on a running process eventually trigger a hard
// "STOP" hint, teaching the model to wait for the automatic completion
// notification instead of polling.
func TestTerminalOutputTool_AntiPollBlocksEmptySnapshots(t *testing.T) {
	m := NewBackgroundManager()
	term := TerminalTool{mgr: m}
	outTool := TerminalOutputTool{mgr: m}

	if _, err := term.Execute(context.Background(), "terminal", map[string]any{
		"command":           "sleep 30",
		"run_in_background": "interactive",
	}); err != nil {
		t.Fatalf("launch: %v", err)
	}

	var blocked bool
	for i := 1; i <= 3; i++ {
		res, err := outTool.Execute(context.Background(), "terminal_output", map[string]any{"id": "bg_1"})
		if err != nil {
			t.Fatalf("poll %d should not error: %v", i, err)
		}
		if strings.Contains(res.Text, "[STOP:") {
			blocked = true
		}
	}
	if !blocked {
		t.Errorf("expected anti-polling STOP after %d empty snapshots", 3)
	}
}

func TestTerminalOutputTool_ReadOnly(t *testing.T) {
	m := NewBackgroundManager()
	term := TerminalTool{mgr: m}
	outTool := TerminalOutputTool{mgr: m}

	if _, err := term.Execute(context.Background(), "terminal", map[string]any{
		"command":           "sleep 30",
		"run_in_background": "interactive",
	}); err != nil {
		t.Fatalf("launch: %v", err)
	}
	// terminal_output no longer kills: a "kill" key is ignored and the process
	// keeps running.
	resOut, err := outTool.Execute(context.Background(), "terminal_output", map[string]any{
		"id":   "bg_1",
		"kill": true, // ignored
	})
	if err != nil {
		t.Fatalf("terminal_output: %v", err)
	}
	if strings.Contains(resOut.Text, "killed") {
		t.Errorf("terminal_output must not kill, got %q", resOut.Text)
	}
	if _, status, _, _, _ := m.Read("bg_1"); status != "running" {
		t.Errorf("process should still be running after terminal_output, status=%q", status)
	}
}

// TestTerminalOutputTool_RejectsAsync verifies that terminal_output refuses to
// observe async (one-shot) background tasks. The model must wait for the
// automatic completion notification instead of polling.
func TestTerminalOutputTool_RejectsAsync(t *testing.T) {
	m := NewBackgroundManager()
	term := TerminalTool{mgr: m}
	outTool := TerminalOutputTool{mgr: m}

	if _, err := term.Execute(context.Background(), "terminal", map[string]any{
		"command":           "sleep 30",
		"run_in_background": "async",
	}); err != nil {
		t.Fatalf("launch: %v", err)
	}

	_, err := outTool.Execute(context.Background(), "terminal_output", map[string]any{"id": "bg_1"})
	if err == nil {
		t.Fatal("terminal_output should reject async processes")
	}
	if !strings.Contains(err.Error(), "async task") {
		t.Errorf("error should explain async rejection, got: %v", err)
	}
}

// TestTerminalInputTool_RejectsAsync verifies that terminal_input refuses to
// send input to async (one-shot) background tasks.
func TestTerminalInputTool_RejectsAsync(t *testing.T) {
	m := NewBackgroundManager()
	term := TerminalTool{mgr: m}
	inTool := TerminalInputTool{mgr: m}

	if _, err := term.Execute(context.Background(), "terminal", map[string]any{
		"command":           "sleep 30",
		"run_in_background": "async",
	}); err != nil {
		t.Fatalf("launch: %v", err)
	}

	_, err := inTool.Execute(context.Background(), "terminal_input", map[string]any{
		"id":    "bg_1",
		"input": "hello\n",
	})
	if err == nil {
		t.Fatal("terminal_input should reject async processes")
	}
	if !strings.Contains(err.Error(), "async task") {
		t.Errorf("error should explain async rejection, got: %v", err)
	}
}

// TestTerminalTool_InvalidBackgroundMode verifies that invalid run_in_background
// values (unknown strings, booleans, or boolean-like strings) surface as clear
// tool errors mentioning the valid enum values.
func TestTerminalTool_InvalidBackgroundMode(t *testing.T) {
	m := NewBackgroundManager()
	term := TerminalTool{mgr: m}

	cases := []struct {
		name string
		val  any
	}{
		{"unknown string", "batch"},
		{"boolean true", true},
		{"boolean false", false},
		{"string true", "true"},
		{"string false", "false"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := term.Execute(context.Background(), "terminal", map[string]any{
				"command":           "echo hi",
				"run_in_background": tc.val,
			})
			if err == nil {
				t.Fatal("invalid run_in_background mode should error")
			}
			if !strings.Contains(err.Error(), "async") || !strings.Contains(err.Error(), "interactive") {
				t.Errorf("error should mention valid modes, got: %v", err)
			}
		})
	}
}
