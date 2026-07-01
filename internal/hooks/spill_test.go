package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// waitFor polls until cond is true or the deadline elapses.
func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestSpill_ToDiskThenRedeliver(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	q := &spillQueue{ch: make(chan asyncItem, 1), dir: dir}

	// Spill an item whose command touches a marker.
	q.spillToDisk(asyncItem{Command: "touch " + marker, Timeout: DefaultTimeout, Payload: Payload{Event: EventStop}})

	// A pending file now exists; the marker does not yet.
	pend, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(pend) != 1 {
		t.Fatalf("expected 1 pending file, got %d", len(pend))
	}
	if fileExists(marker) {
		t.Fatal("command must not have run yet")
	}

	// Redelivery claims, runs, and deletes it.
	q.redeliverPending()
	if !fileExists(marker) {
		t.Error("redeliverPending should have run the spilled command")
	}
	if pend, _ = filepath.Glob(filepath.Join(dir, "*.json")); len(pend) != 0 {
		t.Errorf("pending file should be deleted after redelivery, %d remain", len(pend))
	}
}

func TestSpill_RunPipesPayloadToStdin(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "captured.json")
	q := &spillQueue{ch: make(chan asyncItem, 1), dir: dir}
	q.run(asyncItem{Command: "cat > " + out, Timeout: DefaultTimeout, Payload: Payload{Event: EventStop, AssistantReply: "hi there"}})
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"assistant_reply":"hi there"`) {
		t.Errorf("payload not delivered on stdin: %s", b)
	}
}

func TestSpill_OfferSpillsWhenBacklogFull(t *testing.T) {
	dir := t.TempDir()
	q := &spillQueue{ch: make(chan asyncItem, 1), dir: dir}
	// Fill the single channel slot without a worker draining it.
	q.ch <- asyncItem{Command: "true"}
	// Next offer can't enqueue → must spill to disk.
	q.offer(asyncItem{Command: "true", Payload: Payload{Event: EventStop}})
	if pend, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(pend) != 1 {
		t.Errorf("a full backlog must spill to disk, got %d files", len(pend))
	}
}

func TestSpill_OfferAfterCloseSpills(t *testing.T) {
	dir := t.TempDir()
	q := &spillQueue{ch: make(chan asyncItem, 4), dir: dir, started: true, closed: true}
	q.offer(asyncItem{Command: "true", Payload: Payload{Event: EventStop}})
	if pend, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(pend) != 1 {
		t.Errorf("offer after close must spill, got %d files", len(pend))
	}
}

func TestSpill_AsyncRunsViaWorker(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "async-ran")
	q := &spillQueue{ch: make(chan asyncItem, 4), dir: dir}
	q.ensureStarted() // starts workers + (empty) redelivery
	q.offer(asyncItem{Command: "touch " + marker, Timeout: DefaultTimeout, Payload: Payload{Event: EventStop}})
	if !waitFor(t, 3*time.Second, func() bool { return fileExists(marker) }) {
		t.Error("async worker should have run the hook")
	}
	q.Drain(time.Second)
}

func TestSpill_DrainSpillsUnrunBacklog(t *testing.T) {
	dir := t.TempDir()
	q := &spillQueue{ch: make(chan asyncItem, 8), dir: dir, started: true}
	// Pre-load the channel WITHOUT starting workers, then drain with a zero
	// deadline so nothing gets processed — everything must land on disk.
	for i := 0; i < 5; i++ {
		q.ch <- asyncItem{Command: "true", Payload: Payload{Event: EventStop}}
	}
	q.Drain(1 * time.Millisecond)
	pend, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(pend) != 5 {
		t.Errorf("Drain must spill all %d unrun items; got %d", 5, len(pend))
	}
}
