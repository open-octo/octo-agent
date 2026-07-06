package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInputHistory_LoadMissingFile(t *testing.T) {
	if got := loadInputHistory(filepath.Join(t.TempDir(), "nope")); got != nil {
		t.Errorf("loadInputHistory(missing) = %v, want nil", got)
	}
}

func TestInputHistory_EmptyPathDisablesPersistence(t *testing.T) {
	if got := loadInputHistory(""); got != nil {
		t.Errorf("loadInputHistory(\"\") = %v, want nil", got)
	}
	// Must not panic or create anything relative to cwd.
	appendInputHistoryLine("", "should be a no-op")
}

func TestInputHistory_AppendThenLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "input_history")
	appendInputHistoryLine(path, "first line")
	appendInputHistoryLine(path, "second line")

	got := loadInputHistory(path)
	want := []string{"first line", "second line"}
	if len(got) != len(want) {
		t.Fatalf("loadInputHistory = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestInputHistory_MultilineEntryRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input_history")
	multi := "line one\nline two\nline three"
	appendInputHistoryLine(path, multi)

	got := loadInputHistory(path)
	if len(got) != 1 || got[0] != multi {
		t.Fatalf("loadInputHistory = %q, want single entry %q", got, multi)
	}
	// The file itself must stay one physical line per entry.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := countLines(string(b)); n != 1 {
		t.Errorf("history file has %d physical lines, want 1 (JSON-encoded)", n)
	}
}

func TestInputHistory_LoadCapsAndTrimsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input_history")
	total := inputHistoryCap + 50
	for i := 0; i < total; i++ {
		appendInputHistoryLine(path, "entry")
	}

	got := loadInputHistory(path)
	if len(got) != inputHistoryCap {
		t.Fatalf("loadInputHistory returned %d entries, want cap %d", len(got), inputHistoryCap)
	}

	// The on-disk file should have been rewritten down to the cap too, so a
	// second load (simulating a restart) doesn't keep re-trimming.
	got2 := loadInputHistory(path)
	if len(got2) != inputHistoryCap {
		t.Fatalf("second load returned %d entries, want cap %d", len(got2), inputHistoryCap)
	}
}

func TestInputHistory_SkipsCorruptLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input_history")
	if err := os.WriteFile(path, []byte("not json\n\"valid\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadInputHistory(path)
	if len(got) != 1 || got[0] != "valid" {
		t.Fatalf("loadInputHistory = %v, want [valid]", got)
	}
}

func countLines(s string) int {
	n := 0
	for _, r := range s {
		if r == '\n' {
			n++
		}
	}
	return n
}
