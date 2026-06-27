package tools

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestTerminalListTool lists running background processes so the model can
// recover an id, and reflects status once a process exits.
func TestTerminalListTool(t *testing.T) {
	m := NewBackgroundManager()
	term := TerminalTool{mgr: m}
	listTool := TerminalListTool{mgr: m}
	ctx := context.Background()

	// Nothing running yet.
	if res, _ := listTool.Execute(ctx, "terminal_list", nil); !strings.Contains(res.Text, "No background processes") {
		t.Errorf("empty list should say so, got %q", res.Text)
	}

	if _, err := term.Execute(ctx, "terminal", map[string]any{
		"command":           "sleep 30",
		"run_in_background": "async",
	}); err != nil {
		t.Fatalf("launch: %v", err)
	}

	res, err := listTool.Execute(ctx, "terminal_list", nil)
	if err != nil {
		t.Fatalf("terminal_list: %v", err)
	}
	if !strings.Contains(res.Text, "bg_1") || !strings.Contains(res.Text, "running") {
		t.Errorf("list should show the running process id + status, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "sleep 30") {
		t.Errorf("list should include the command, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "[async]") {
		t.Errorf("list should include the mode, got %q", res.Text)
	}
}

// TestTerminalListTool_RunningAsyncReminder verifies that terminal_list adds
// a system-reminder telling the model not to poll when async tasks are still
// running. Interactive-only lists do not trigger the reminder.
func TestTerminalListTool_RunningAsyncReminder(t *testing.T) {
	m := NewBackgroundManager()
	term := TerminalTool{mgr: m}
	listTool := TerminalListTool{mgr: m}
	ctx := context.Background()

	// Interactive-only list: no reminder.
	if _, err := term.Execute(ctx, "terminal", map[string]any{
		"command":           "sleep 30",
		"run_in_background": "interactive",
	}); err != nil {
		t.Fatalf("launch: %v", err)
	}
	res, err := listTool.Execute(ctx, "terminal_list", nil)
	if err != nil {
		t.Fatalf("terminal_list: %v", err)
	}
	if strings.Contains(res.Text, "[BACKGROUND COMPLETED]") {
		t.Errorf("interactive-only list should not include the async reminder, got %q", res.Text)
	}

	// Adding a running async task triggers the reminder.
	if _, err := term.Execute(ctx, "terminal", map[string]any{
		"command":           "sleep 30",
		"run_in_background": "async",
	}); err != nil {
		t.Fatalf("launch: %v", err)
	}
	res2, err := listTool.Execute(ctx, "terminal_list", nil)
	if err != nil {
		t.Fatalf("terminal_list: %v", err)
	}
	if !strings.Contains(res2.Text, "[BACKGROUND COMPLETED]") {
		t.Errorf("running async process should trigger a reminder not to poll, got %q", res2.Text)
	}
}

// TestTerminalListTool_ExitedElapsedFrozen verifies that once a background
// process exits, terminal_list reports its actual lifetime rather than the time
// elapsed since the process started.
func TestTerminalListTool_ExitedElapsedFrozen(t *testing.T) {
	m := NewBackgroundManager()
	term := TerminalTool{mgr: m}
	listTool := TerminalListTool{mgr: m}
	ctx := context.Background()

	if _, err := term.Execute(ctx, "terminal", map[string]any{
		"command":           "sleep 0.1",
		"run_in_background": "async",
	}); err != nil {
		t.Fatalf("launch: %v", err)
	}

	waitFor(t, "exit", func() bool {
		_, s, _, _, _ := m.Read("bg_1")
		return strings.HasPrefix(s, "exited")
	})

	first := listAndParseElapsed(t, listTool, ctx, "bg_1")

	// Wait and list again: the elapsed time for an exited process should not
	// grow, because it is computed from the recorded end time.
	time.Sleep(200 * time.Millisecond)
	second := listAndParseElapsed(t, listTool, ctx, "bg_1")

	if second != first {
		t.Errorf("exited process elapsed should be frozen, got %v then %v", first, second)
	}

	res, err := listTool.Execute(ctx, "terminal_list", nil)
	if err != nil {
		t.Fatalf("terminal_list: %v", err)
	}
	if !strings.Contains(res.Text, "exited:") {
		t.Errorf("list should show exited status, got %q", res.Text)
	}
}

// listAndParseElapsed runs terminal_list and extracts the elapsed duration for
// the given process id. It expects lines of the form:
//
//	bg_1  [async]  [exited: 0]  100ms  sleep 0.1
var elapsedRE = regexp.MustCompile(`^(\S+)\s+\[[^\]]+\]\s+\[[^\]]+\]\s+(\S+)\s+`)

func listAndParseElapsed(t *testing.T, listTool TerminalListTool, ctx context.Context, id string) time.Duration {
	t.Helper()
	res, err := listTool.Execute(ctx, "terminal_list", nil)
	if err != nil {
		t.Fatalf("terminal_list: %v", err)
	}
	for _, line := range strings.Split(res.Text, "\n") {
		m := elapsedRE.FindStringSubmatch(line)
		if m == nil || m[1] != id {
			continue
		}
		d, err := time.ParseDuration(m[2])
		if err == nil {
			return d
		}
		// go's time.Duration String() may return values like "0s"; try strconv
		// for seconds in case the format ever changes.
		if s, convErr := strconv.ParseFloat(m[2], 64); convErr == nil {
			return time.Duration(s * float64(time.Second))
		}
		t.Fatalf("could not parse elapsed %q: %v", m[2], err)
	}
	t.Fatalf("process %q not found in list output: %q", id, res.Text)
	return 0
}

// TestTerminalOutputTool_LinesSnapshot verifies the lines param trims to the
// last N lines and that the snapshot is non-advancing (a second call returns
// the same tail, not "new since last read").
func TestTerminalOutputTool_LinesSnapshot(t *testing.T) {
	m := NewBackgroundManager()
	outTool := TerminalOutputTool{mgr: m}
	ctx := context.Background()

	// Print 5 lines then exit; wait for completion.
	if _, err := (TerminalTool{mgr: m}).Execute(ctx, "terminal", map[string]any{
		"command":           "printf 'l1\\nl2\\nl3\\nl4\\nl5\\n'",
		"run_in_background": "interactive",
	}); err != nil {
		t.Fatalf("launch: %v", err)
	}
	waitFor(t, "exit", func() bool {
		_, s, _, _, _ := m.Read("bg_1")
		return strings.HasPrefix(s, "exited")
	})

	// Last 2 lines only.
	res, err := outTool.Execute(ctx, "terminal_output", map[string]any{"id": "bg_1", "lines": float64(2)})
	if err != nil {
		t.Fatalf("terminal_output: %v", err)
	}
	if !strings.Contains(res.Text, "l4") || !strings.Contains(res.Text, "l5") {
		t.Errorf("tail(2) should contain the last two lines, got %q", res.Text)
	}
	if strings.Contains(res.Text, "l1") {
		t.Errorf("tail(2) should not contain the first line, got %q", res.Text)
	}

	// Idempotent: a second identical call returns the same snapshot.
	res2, _ := outTool.Execute(ctx, "terminal_output", map[string]any{"id": "bg_1", "lines": float64(2)})
	if res2.Text != res.Text {
		t.Errorf("snapshot should be idempotent:\nfirst:  %q\nsecond: %q", res.Text, res2.Text)
	}
}
