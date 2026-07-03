package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteCardSpill_PathAndContent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path, err := WriteCardSpill("mcp_thing", "line1\nline2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.Base(path), "card-mcp_thing-") {
		t.Errorf("unexpected spill name: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "line1\nline2" {
		t.Errorf("spill content = %q, err %v", data, err)
	}
	// A second write in the same process must not collide.
	path2, err := WriteCardSpill("mcp_thing", "other")
	if err != nil || path2 == path {
		t.Errorf("second spill should get a fresh name: %s vs %s (err %v)", path, path2, err)
	}
}

func TestCleanSpillFiles_KeepsCardSpills(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cardPath, err := WriteCardSpill("terminal", "full output")
	if err != nil {
		t.Fatal(err)
	}
	termPath, err := writeSpillFile("bg_1", "model-facing")
	if err != nil {
		t.Fatal(err)
	}
	CleanSpillFiles()
	if _, err := os.Stat(termPath); !os.IsNotExist(err) {
		t.Error("session-scoped term- spill should be removed on exit")
	}
	// The card spill is hyperlinked from the terminal scrollback, which
	// outlives the process — it must survive shutdown.
	if _, err := os.Stat(cardPath); err != nil {
		t.Errorf("card spill should survive CleanSpillFiles: %v", err)
	}
}

func TestSweepReclaimsOldCardSpills(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path, err := WriteCardSpill("terminal", "old")
	if err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * spillMaxAge)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}
	sweepOldSpillFiles(filepath.Dir(path))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("age sweep should reclaim old card spills")
	}
}
