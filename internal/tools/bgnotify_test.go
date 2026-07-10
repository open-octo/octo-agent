package tools

import (
	"strings"
	"testing"
	"time"
)

func TestFormatBgNote_WrapsAsSystemReminder(t *testing.T) {
	got := FormatBgNote(BgExit{
		ID:        "bg_1",
		Command:   "go test ./...",
		Status:    "exited: 0",
		NewOutput: "ok  github.com/x/y  1.2s\n",
	})
	for _, want := range []string{
		"<system-reminder>", "</system-reminder>",
		"bg_1", "go test ./...", "exited: 0", "ok  github.com/x/y  1.2s",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("FormatBgNote missing %q in:\n%s", want, got)
		}
	}
}

func TestFormatBgNote_NoOutput(t *testing.T) {
	got := FormatBgNote(BgExit{ID: "bg_2", Command: "true", Status: "exited: 0"})
	if !strings.Contains(got, "(no new output)") {
		t.Errorf("want '(no new output)' marker; got:\n%s", got)
	}
}

func TestFormatBgNote_CarriesNonZeroExit(t *testing.T) {
	got := FormatBgNote(BgExit{ID: "bg_3", Command: "false", Status: "exited: exit status 1"})
	if !strings.Contains(got, "exit status 1") {
		t.Errorf("want the non-zero exit surfaced; got:\n%s", got)
	}
}

// TestFormatBgNote_NudgesUnnecessaryAsync verifies that an "async" launch
// which finished well under shortAsyncDuration gets a corrective note: the
// whole point of async is to background work you expect to run long, and a
// launch that turns out to finish in a couple of seconds would have
// returned this same output synchronously, in the same tool call, with none
// of the background bookkeeping.
func TestFormatBgNote_NudgesUnnecessaryAsync(t *testing.T) {
	got := FormatBgNote(BgExit{
		ID: "bg_1", Command: "go build ./...", Status: "exited: 0",
		NewOutput: "ok\n", Mode: BgModeAsync, Duration: 2 * time.Second,
	})
	if !strings.Contains(got, "didn't need run_in_background") {
		t.Errorf("expected the short-async nudge; got:\n%s", got)
	}
}

// TestFormatBgNote_NoNudgeForGenuinelyLongAsync verifies a long-running async
// task (the case the mode exists for) does not get flagged.
func TestFormatBgNote_NoNudgeForGenuinelyLongAsync(t *testing.T) {
	got := FormatBgNote(BgExit{
		ID: "bg_1", Command: "go build ./...", Status: "exited: 0",
		NewOutput: "ok\n", Mode: BgModeAsync, Duration: 90 * time.Second,
	})
	if strings.Contains(got, "didn't need run_in_background") {
		t.Errorf("did not expect the short-async nudge for a long-running task; got:\n%s", got)
	}
}

// TestFormatBgNote_NoNudgeForInteractive verifies interactive processes never
// get the nudge, even if they exit quickly — an interactive service that
// exits fast is more likely a crash than a mode-choice mistake, and the mode
// exists for services that are *meant* to run indefinitely, not one-shot
// results, so "should have been sync" doesn't apply to it the same way.
func TestFormatBgNote_NoNudgeForInteractive(t *testing.T) {
	got := FormatBgNote(BgExit{
		ID: "bg_1", Command: "octo serve", Status: "exited: 1",
		NewOutput: "address already in use\n", Mode: BgModeInteractive, Duration: 1 * time.Second,
	})
	if strings.Contains(got, "didn't need run_in_background") {
		t.Errorf("did not expect the short-async nudge for an interactive process; got:\n%s", got)
	}
}

// TestFormatBgNote_NoNudgeWithZeroDuration guards the Duration>0 check: a
// caller that forgets to populate Duration (e.g. an older test fixture or a
// future call site) must not get a false-positive nudge from the zero value.
func TestFormatBgNote_NoNudgeWithZeroDuration(t *testing.T) {
	got := FormatBgNote(BgExit{ID: "bg_1", Command: "true", Status: "exited: 0", Mode: BgModeAsync})
	if strings.Contains(got, "didn't need run_in_background") {
		t.Errorf("zero Duration must not trigger the nudge; got:\n%s", got)
	}
}

// TestFormatBgNote_NoNudgeForKilled verifies a process that was KILLED quickly
// does not get the short-async nudge. Its Duration is time-until-kill, so a
// `sleep 118` reaped at 4s would otherwise be mislabeled "finished in 4s —
// didn't need run_in_background" when it never finished at all.
func TestFormatBgNote_NoNudgeForKilled(t *testing.T) {
	got := FormatBgNote(BgExit{
		ID: "bg_1", Command: "sleep 118", Status: "exited: signal: killed",
		Mode: BgModeAsync, Duration: 4 * time.Second,
	})
	if strings.Contains(got, "didn't need run_in_background") {
		t.Errorf("a killed process must not get the short-async nudge; got:\n%s", got)
	}
}

func TestFormatBgNoteWithSummary_SkipsFinishedAndSelf(t *testing.T) {
	mgr := NewBackgroundManager()
	// Launch three processes; we will simulate bg_1 finishing.
	_, err := mgr.Start("echo one", BgModeAsync)
	if err != nil {
		t.Fatalf("Start bg_1: %v", err)
	}
	_, err = mgr.Start("sleep 60", BgModeAsync)
	if err != nil {
		t.Fatalf("Start bg_2: %v", err)
	}
	_, err = mgr.Start("node server.js", BgModeInteractive)
	if err != nil {
		t.Fatalf("Start bg_3: %v", err)
	}

	got := FormatBgNoteWithSummary(mgr, BgExit{ID: "bg_1", Command: "echo one", Status: "exited: 0"})
	if !strings.Contains(got, "Still running:") {
		t.Errorf("want summary; got:\n%s", got)
	}
	if !strings.Contains(got, "1 async") {
		t.Errorf("want 1 async still running; got:\n%s", got)
	}
	if !strings.Contains(got, "1 interactive") {
		t.Errorf("want 1 interactive still running; got:\n%s", got)
	}
	// The finished process must not be listed as still running.
	if strings.Contains(got, "bg_1 `echo one`") {
		t.Errorf("finished process bg_1 should not appear in the still-running summary; got:\n%s", got)
	}
}

func TestFormatBgNoteWithSummary_NoOthers(t *testing.T) {
	mgr := NewBackgroundManager()
	got := FormatBgNoteWithSummary(mgr, BgExit{ID: "bg_1", Command: "echo done", Status: "exited: 0"})
	if strings.Contains(got, "Still running:") {
		t.Errorf("no summary expected when nothing else is running; got:\n%s", got)
	}
}
