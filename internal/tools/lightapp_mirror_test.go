package tools

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// TestLightAppMirror_EvictsAbandoned: the DELETE the host sends on unmount is
// the normal way out, and it is exactly the one that never arrives when a
// laptop closes. Without eviction that leaves a 12 MiB screenshot and an app
// the model keeps being told is open.
func TestLightAppMirror_EvictsAbandoned(t *testing.T) {
	resetLightAppMirror(t)
	PutLightApp(LightAppSnapshot{
		Slug:      "ghost",
		Digest:    "a canvas nobody has open",
		Image:     tinyPNG(t),
		UpdatedAt: time.Now().Add(-2 * lightAppEvictAfter),
	})
	PutLightApp(LightAppSnapshot{Slug: "live", Digest: "still open"})

	out := runTool(t, LightAppStateTool{}, nil)
	if strings.Contains(out, "ghost") {
		t.Errorf("an abandoned app is still being reported:\n%s", out)
	}
	if !strings.Contains(out, "live") {
		t.Errorf("eviction took a live app with it:\n%s", out)
	}
	if lightAppSnapshot("ghost") != nil {
		t.Error("the abandoned snapshot is still held in memory")
	}
}

// TestLightAppMirror_StaleIsNotEvicted: the two thresholds mean different
// things — a canvas the user stepped away from for ten minutes is still theirs,
// it just gets labelled.
func TestLightAppMirror_StaleIsNotEvicted(t *testing.T) {
	resetLightAppMirror(t)
	PutLightApp(LightAppSnapshot{
		Slug:      "sketch",
		Digest:    "drawn a while ago",
		UpdatedAt: time.Now().Add(-2 * lightAppStaleAfter),
	})

	out := runTool(t, LightAppStateTool{}, nil)
	if !strings.Contains(out, "sketch") {
		t.Fatalf("a stale app should still be reported:\n%s", out)
	}
	if !strings.Contains(out, "stale") {
		t.Errorf("and it should be labelled stale:\n%s", out)
	}
}

// TestLightAppMirror_ConcurrentAccess runs the process-wide state from several
// goroutines at once. Worth having because -race is the only thing that
// actually checks the locking, and the HTTP handler and the tools reach this
// map from different ones.
func TestLightAppMirror_ConcurrentAccess(t *testing.T) {
	resetLightAppMirror(t)
	var wg sync.WaitGroup
	for i := range 8 {
		slug := fmt.Sprintf("app%d", i%3)
		wg.Add(3)
		go func() {
			defer wg.Done()
			PutLightApp(LightAppSnapshot{Slug: slug, Digest: "x"})
		}()
		go func() {
			defer wg.Done()
			_ = lightAppSnapshots()
		}()
		go func() {
			defer wg.Done()
			DropLightApp(slug)
		}()
	}
	wg.Wait()
}

// TestDigestTruncation_KeepsValidUTF8: a digest is prose, often not ASCII, and
// the cap is in bytes.
func TestDigestTruncation_KeepsValidUTF8(t *testing.T) {
	resetLightAppMirror(t)
	PutLightApp(LightAppSnapshot{Slug: "sketch", Digest: strings.Repeat("一", maxLightAppDigest)})

	snap := lightAppSnapshot("sketch")
	if snap == nil {
		t.Fatal("snapshot was rejected")
	}
	if !utf8.ValidString(snap.Digest) {
		t.Error("truncation cut a rune in half")
	}
}
