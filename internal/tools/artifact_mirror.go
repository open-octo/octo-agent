package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ─── Artifact state mirror ──────────────────────────────────────────────────
//
// An artifact (an HTML page the agent wrote, open in the session's panel)
// runs in an iframe on its own origin, behind a CSP that closes every network
// exit it has (artifact_gate.go). It cannot call octo's API, and that is the
// point — it is why an artifact may run whatever code it likes.
//
// So the page pushes instead: a compact snapshot goes out over the postMessage
// bridge, the web UI relays it to PUT /api/sessions/{id}/artifacts/state, and
// this process-wide mirror is what the model's tools read. The page never
// gains a capability; it only gets to speak.
//
// Keyed by (session, path): the same file open in two sessions is two
// snapshots, and a tool only ever lists its own session's. Nothing here is
// persisted — the mirror dies with the serve process, which matches what it
// holds: whatever happens to be on screen right now.

// artifactStaleAfter is how long a snapshot stays current. Past it the tools
// still answer, but they say the page may have moved on — better than a model
// reasoning confidently about a rendering from an hour ago.
const artifactStaleAfter = 5 * time.Minute

// artifactEvictAfter is when a snapshot stops being reported at all. The
// explicit DELETE the host sends on unmount is the normal path out; this is
// for the ones that never arrive — a closed laptop, a killed browser, a tab
// discarded under memory pressure. Without it a 12 MiB screenshot and a
// phantom page would both live until serve restarts, and insert_into_artifact
// would happily report a delivery into a frame nobody has open.
const artifactEvictAfter = 30 * time.Minute

const (
	maxArtifactDigest  = 4 << 10  // 4 KiB of prose is already more than a digest
	maxArtifactSummary = 32 << 10 // structured extras, passed through verbatim
	maxArtifactImage   = 12 << 20 // a rendered-page screenshot, before compression
)

// ArtifactSnapshot is one push from an open artifact page.
type ArtifactSnapshot struct {
	// Session and Path are the artifact's identity — the same pair
	// /api/sessions/{id}/artifacts?path=… serves by.
	Session string
	Path    string
	// Digest is the page's own one-line description of its state. The page
	// knows what is inside it far better than the host does, so the host
	// never tries to derive this.
	Digest string
	// Summary is opaque structured data the page chose to include. Relayed to
	// the model as JSON, never interpreted here.
	Summary json.RawMessage
	// Image is an optional screenshot, held in memory only — see PutArtifact.
	Image     []byte
	ImageType string
	UpdatedAt time.Time
}

// Stale reports whether the snapshot is old enough that the model should be
// told so.
func (s *ArtifactSnapshot) Stale(now time.Time) bool {
	return now.Sub(s.UpdatedAt) > artifactStaleAfter
}

type artifactKey struct {
	session string
	path    string
}

var artifactMirror struct {
	mu   sync.Mutex
	apps map[artifactKey]*ArtifactSnapshot
}

// PutArtifact records the latest snapshot for one open page, replacing any
// previous one. Oversized fields are truncated rather than rejected: a
// snapshot is a courtesy from the page, and half a digest still beats no
// digest.
//
// The screenshot is kept in memory and nowhere else. Most are replaced within
// seconds or die when the panel closes, and octo's upload store is
// content-addressed with no deletion — writing every frame to it would leak
// disk forever. It reaches disk only if view_artifact actually hands it to the
// model.
func PutArtifact(snap ArtifactSnapshot) {
	if snap.Session == "" || snap.Path == "" {
		return
	}
	if len(snap.Digest) > maxArtifactDigest {
		// Cut on a rune boundary: a digest is prose, often not ASCII, and
		// slicing bytes would leave a half character for json.Marshal to
		// replace with U+FFFD.
		snap.Digest = strings.ToValidUTF8(snap.Digest[:maxArtifactDigest], "") + "…"
	}
	if len(snap.Summary) > maxArtifactSummary {
		snap.Summary = nil
	}
	if len(snap.Image) > maxArtifactImage {
		snap.Image = nil
		snap.ImageType = ""
	}
	if snap.UpdatedAt.IsZero() {
		snap.UpdatedAt = time.Now()
	}

	artifactMirror.mu.Lock()
	defer artifactMirror.mu.Unlock()
	if artifactMirror.apps == nil {
		artifactMirror.apps = map[artifactKey]*ArtifactSnapshot{}
	}
	evictArtifactsLocked(time.Now())
	artifactMirror.apps[artifactKey{snap.Session, snap.Path}] = &snap
}

// evictArtifactsLocked drops snapshots nobody has refreshed in a long time.
// Lazy rather than a timer: the mirror is only interesting when something
// touches it, and every path that reads or writes it already holds the lock.
func evictArtifactsLocked(now time.Time) {
	for k, s := range artifactMirror.apps {
		if now.Sub(s.UpdatedAt) > artifactEvictAfter {
			delete(artifactMirror.apps, k)
		}
	}
}

// DropArtifact forgets one page — sent when its frame goes away, so the tools
// stop describing something nobody is looking at.
func DropArtifact(session, path string) {
	artifactMirror.mu.Lock()
	defer artifactMirror.mu.Unlock()
	delete(artifactMirror.apps, artifactKey{session, path})
}

// artifactSnapshot returns a copy of one page's snapshot, or nil.
func artifactSnapshot(session, path string) *ArtifactSnapshot {
	artifactMirror.mu.Lock()
	defer artifactMirror.mu.Unlock()
	evictArtifactsLocked(time.Now())
	s := artifactMirror.apps[artifactKey{session, path}]
	if s == nil {
		return nil
	}
	cp := *s
	return &cp
}

// artifactSnapshots returns every reporting page of one session, path order.
func artifactSnapshots(session string) []*ArtifactSnapshot {
	artifactMirror.mu.Lock()
	defer artifactMirror.mu.Unlock()
	evictArtifactsLocked(time.Now())
	out := make([]*ArtifactSnapshot, 0, len(artifactMirror.apps))
	for k, s := range artifactMirror.apps {
		if k.session != session {
			continue
		}
		cp := *s
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// describeAge renders the "updated N ago" line the digest carries.
func describeAge(d time.Duration) string {
	switch {
	case d < 2*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

// renderedSummaryMax is what one page may contribute to an artifact_state
// answer. The ingest cap (maxArtifactSummary) is about what the mirror will
// hold; this is about what a tool the model is told to call freely may spend
// of its context.
const renderedSummaryMax = 512

// renderArtifactLine is one page's line in the artifact_state answer.
//
// The digest and summary are written by the page, which is generated code —
// so they are attributed and quoted rather than stated in octo's own voice:
// everything indented under the page's name is the page describing itself,
// not octo reporting a fact.
func renderArtifactLine(s *ArtifactSnapshot, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- %s — updated %s", s.Path, describeAge(now.Sub(s.UpdatedAt)))
	if s.Stale(now) {
		b.WriteString(" (stale: the user may have moved on)")
	}
	if len(s.Image) > 0 {
		b.WriteString(", has a screenshot you can view")
	}
	b.WriteString("\n")
	if s.Digest != "" {
		fmt.Fprintf(&b, "  the page says: %q\n", s.Digest)
	}
	if len(s.Summary) > 0 {
		sum := string(s.Summary)
		if len(sum) > renderedSummaryMax {
			sum = strings.ToValidUTF8(sum[:renderedSummaryMax], "") + "… (truncated)"
		}
		fmt.Fprintf(&b, "  and reports: %s\n", sum)
	}
	return b.String()
}
