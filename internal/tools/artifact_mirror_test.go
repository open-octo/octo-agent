package tools

import (
	"strings"
	"testing"
	"time"
)

// resetArtifactMirror clears the process-wide mirror between cases.
func resetArtifactMirror(t *testing.T) {
	t.Helper()
	artifactMirror.mu.Lock()
	artifactMirror.apps = nil
	artifactMirror.mu.Unlock()
}

// TestArtifactMirror_ScopedBySession: the same artifact path can be open in
// two sessions at once, and each session's tools must only ever see their
// own. The mirror key is (session, path) — anything weaker lets one
// conversation's agent peek at another's panel.
func TestArtifactMirror_ScopedBySession(t *testing.T) {
	resetArtifactMirror(t)
	PutArtifact(ArtifactSnapshot{Session: "s1", Path: "/tmp/a.html", Digest: "session one's page"})
	PutArtifact(ArtifactSnapshot{Session: "s2", Path: "/tmp/a.html", Digest: "session two's page"})

	one := artifactSnapshots("s1")
	if len(one) != 1 || one[0].Digest != "session one's page" {
		t.Fatalf("s1 sees %+v", one)
	}
	if got := artifactSnapshot("s2", "/tmp/a.html"); got == nil || got.Digest != "session two's page" {
		t.Fatalf("s2's snapshot was crossed: %+v", got)
	}
	if artifactSnapshot("s1", "/tmp/other.html") != nil {
		t.Error("a path s1 never pushed resolved to something")
	}
}

// TestArtifactMirror_Drop: the host's DELETE on iframe unmount is the normal
// way out — the tools must stop describing a page nobody has open.
func TestArtifactMirror_Drop(t *testing.T) {
	resetArtifactMirror(t)
	PutArtifact(ArtifactSnapshot{Session: "s1", Path: "/tmp/a.html", Digest: "open"})
	DropArtifact("s1", "/tmp/a.html")
	if artifactSnapshot("s1", "/tmp/a.html") != nil {
		t.Error("dropped snapshot is still held")
	}
	if len(artifactSnapshots("s1")) != 0 {
		t.Error("dropped snapshot is still listed")
	}
}

// TestArtifactMirror_EvictsAbandoned: the DELETE the host sends on unmount is
// exactly the message that never arrives when a laptop closes. Without
// eviction that leaves a 12 MiB screenshot and a page the model keeps being
// told is open.
func TestArtifactMirror_EvictsAbandoned(t *testing.T) {
	resetArtifactMirror(t)
	PutArtifact(ArtifactSnapshot{
		Session:   "s1",
		Path:      "/tmp/ghost.html",
		Digest:    "a page nobody has open",
		Image:     tinyPNG(t),
		UpdatedAt: time.Now().Add(-2 * artifactEvictAfter),
	})
	PutArtifact(ArtifactSnapshot{Session: "s1", Path: "/tmp/live.html", Digest: "still open"})

	out := artifactSnapshots("s1")
	if len(out) != 1 || out[0].Path != "/tmp/live.html" {
		t.Fatalf("eviction took the live page with it or kept the ghost: %+v", out)
	}
	if artifactSnapshot("s1", "/tmp/ghost.html") != nil {
		t.Error("the abandoned snapshot is still held in memory")
	}
}

// TestArtifactMirror_StaleIsNotEvicted: the two thresholds mean different
// things — a page the user stepped away from for ten minutes is still theirs,
// it just gets labelled.
func TestArtifactMirror_StaleIsNotEvicted(t *testing.T) {
	resetArtifactMirror(t)
	PutArtifact(ArtifactSnapshot{
		Session:   "s1",
		Path:      "/tmp/a.html",
		Digest:    "viewed a while ago",
		UpdatedAt: time.Now().Add(-2 * artifactStaleAfter),
	})

	out := artifactSnapshots("s1")
	if len(out) != 1 {
		t.Fatalf("a stale page should still be listed: %+v", out)
	}
	if !out[0].Stale(time.Now()) {
		t.Error("and it should report itself stale")
	}
}

// TestArtifactMirror_Truncates: a snapshot is a courtesy from the page —
// oversized fields are cut, not rejected. The digest cuts on a rune boundary.
func TestArtifactMirror_Truncates(t *testing.T) {
	resetArtifactMirror(t)
	PutArtifact(ArtifactSnapshot{
		Session: "s1",
		Path:    "/tmp/a.html",
		Digest:  strings.Repeat("界", maxArtifactDigest),
		Image:   make([]byte, maxArtifactImage+1),
	})
	got := artifactSnapshot("s1", "/tmp/a.html")
	if got == nil {
		t.Fatal("snapshot missing")
	}
	if len(got.Image) != 0 {
		t.Error("oversized screenshot should be dropped, not stored")
	}
	if !strings.HasSuffix(got.Digest, "…") {
		t.Error("oversized digest should be truncated with an ellipsis")
	}
}
