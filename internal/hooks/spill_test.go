package hooks

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSpill_ConcurrentOfferDrainNoPanic guards the critical send-on-closed race:
// many offers racing a Drain that closes the channel must never panic. Run under
// -race. Pre-fix, an offer that passed the closed-check then sent after Drain's
// close() would panic "send on closed channel".
func TestSpill_ConcurrentOfferDrainNoPanic(t *testing.T) {
	dir := t.TempDir()
	q := &spillQueue{ch: make(chan asyncItem, 2), dir: dir, started: true}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q.offer(asyncItem{Command: "x", Payload: Payload{Event: EventStop}})
		}()
	}
	q.Drain(20 * time.Millisecond) // closes q.ch while offers are in flight
	wg.Wait()                      // no panic == pass
}

func TestSpill_ReclaimStaleClaim(t *testing.T) {
	dir := t.TempDir()
	q := &spillQueue{dir: dir}

	// An orphaned claim aged past staleClaimAge is reclaimed to <id>.json.
	stale := filepath.Join(dir, "abc.json.claimed.999")
	if err := os.WriteFile(stale, []byte(`{"command":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleClaimAge)
	_ = os.Chtimes(stale, old, old)

	// A fresh claim (a live sibling's in-flight work) must NOT be reclaimed.
	fresh := filepath.Join(dir, "def.json.claimed.888")
	if err := os.WriteFile(fresh, []byte(`{"command":"y"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	q.reclaimStaleClaims()

	if fileExists(stale) {
		t.Error("stale claim should have been reclaimed")
	}
	if !fileExists(filepath.Join(dir, "abc.json")) {
		t.Error("stale claim should be renamed back to .json")
	}
	if !fileExists(fresh) {
		t.Error("a fresh claim must not be reclaimed (would steal a live sibling's work)")
	}
}

func TestSpill_RunUsesOriginatingCwd(t *testing.T) {
	posixOnly(t)
	runDir := t.TempDir()
	q := &spillQueue{dir: t.TempDir()}
	// Relative command; must execute in item.Cwd, not the process cwd.
	q.run(asyncItem{Command: "touch marker", Timeout: DefaultTimeout, Cwd: runDir, Payload: Payload{Event: EventStop}})
	if !fileExists(filepath.Join(runDir, "marker")) {
		t.Error("async hook should run in item.Cwd (confused-deputy fix)")
	}
}

// posixOnly skips tests that execute POSIX shell commands (touch/cat/…): the
// hook runner uses PowerShell on Windows, where those commands don't exist.
// Matches makeScript's Windows trade-off.
func posixOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("spill test runs POSIX shell commands; not portable to Windows")
	}
}

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
	posixOnly(t)
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
	posixOnly(t)
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
	posixOnly(t)
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
