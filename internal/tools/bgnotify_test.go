package tools

import (
	"strings"
	"testing"
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
