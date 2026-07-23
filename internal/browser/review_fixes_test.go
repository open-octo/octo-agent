package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// TestUnsubClosesChannel: a subscription's unsub closes its channel so a
// `for ev := range ch` consumer terminates (it used to only delete from the map,
// leaving such goroutines blocked forever). Double-unsub must not panic.
func TestUnsubClosesChannel(t *testing.T) {
	c := &cdpClient{subs: map[int]*subscription{}}
	ch, unsub := c.subscribe("Some.event", "")
	unsub()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected the channel to be closed after unsub")
		}
	default:
		t.Fatal("channel was not closed by unsub")
	}
	unsub() // sync.Once: must be a no-op, not a close-of-closed panic
}

// TestClaimSessionRaceFree: concurrent claimSession calls for one session yield
// exactly one winner (the guarded compare-and-set the recorder relies on), and a
// released session can be claimed again. Run with -race to catch unguarded map
// access.
func TestClaimSessionRaceFree(t *testing.T) {
	r := NewRecorder(&Page{})
	var wins int64
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r.claimSession("sess-1") {
				atomic.AddInt64(&wins, 1)
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("expected exactly one claim to win, got %d", wins)
	}
	if r.claimSession("sess-1") {
		t.Fatal("already-claimed session should not be claimable again")
	}
	r.releaseSession("sess-1")
	if !r.claimSession("sess-1") {
		t.Fatal("released session should be claimable again")
	}
}

// TestRenderTraceRedactsSecret: the LLM distiller prompt never carries a secret
// field's value, even if one reached RecordedEvent.Value — so the plaintext can't
// be transmitted off-machine or re-inlined into the saved recording.
func TestRenderTraceRedactsSecret(t *testing.T) {
	events := []RecordedEvent{
		{Type: "change", Selector: "#u", Tag: "INPUT", Field: "user", Value: "alice"},
		{Type: "change", Selector: "#pw", Tag: "INPUT", Field: "password", Secret: true, Value: "hunter2"},
	}
	// renderTrace directly.
	if tr := renderTrace(events); strings.Contains(tr, "hunter2") {
		t.Fatalf("renderTrace leaked the secret value:\n%s", tr)
	}
	// End to end: whatever prompt GenerateRecording would send the model.
	var captured string
	gen := func(_ context.Context, _, user string) (string, error) {
		captured = user
		return "", fmt.Errorf("stop") // force fallback to the deterministic baseline
	}
	GenerateRecording(context.Background(), "demo", "https://x/start", events, gen)
	if strings.Contains(captured, "hunter2") {
		t.Fatalf("secret value reached the LLM distiller prompt:\n%s", captured)
	}
	// The non-secret value is still present (redaction is targeted).
	if !strings.Contains(captured, "alice") {
		t.Fatalf("expected the non-secret value to remain in the prompt:\n%s", captured)
	}
}

// TestNormalizeUploadFiles: paths reaching DOM.setFileInputFiles must be
// absolute and existing — Chrome treats anything else (an unexpanded "~", a
// relative path, a missing file) as a compromised renderer and KILLS the page
// (RESULT_CODE_KILLED_BAD_MESSAGE), stranding the whole session. Observed live:
// replaying an upload step with "~/Downloads/…" crashed the tab and hung the
// replay until the turn's 400s deadline.
func TestNormalizeUploadFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "doc.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := normalizeUploadFiles([]string{"~/doc.md"})
	if err != nil {
		t.Fatalf("tilde path should expand and validate: %v", err)
	}
	if len(got) != 1 || got[0] != filepath.Join(home, "doc.md") {
		t.Fatalf("bad expansion: %v", got)
	}

	if _, err := normalizeUploadFiles([]string{"relative/doc.md"}); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative path must be rejected, got: %v", err)
	}
	if _, err := normalizeUploadFiles([]string{filepath.Join(home, "missing.md")}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing file must be rejected, got: %v", err)
	}
	if _, err := normalizeUploadFiles([]string{"~/missing.md"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing tilde file must be rejected, got: %v", err)
	}
}
