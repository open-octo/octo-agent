package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ─── Light App state mirror ─────────────────────────────────────────────────
//
// A Light App runs in an iframe on its own origin, behind a CSP that closes
// every network exit it has (artifact_gate.go). It cannot call octo's API, and
// that is the point — it is why a Light App may run whatever code it likes.
//
// So the app pushes instead: a compact snapshot goes out over the postMessage
// bridge, the web UI relays it to POST /api/light-apps/{slug}/state, and this
// process-wide mirror is what the model's tools read. The app never gains a
// capability; it only gets to speak.
//
// Nothing here is persisted. The mirror dies with the serve process, which
// matches what it holds: whatever happens to be on a canvas right now.

// lightAppStaleAfter is how long a snapshot stays current. Past it the tools
// still answer, but they say the app may have moved on — better than a model
// reasoning confidently about a sketch from an hour ago.
const lightAppStaleAfter = 5 * time.Minute

// lightAppEvictAfter is when a snapshot stops being reported at all. The
// explicit DELETE the host sends on unmount is the normal path out; this is
// for the ones that never arrive — a closed laptop, a killed browser, a tab
// discarded under memory pressure. Without it a 12 MiB screenshot and a
// phantom app would both live until serve restarts, and insert_into_lightapp
// would happily report a delivery into a frame nobody has open.
const lightAppEvictAfter = 30 * time.Minute

const (
	maxLightAppDigest  = 4 << 10  // 4 KiB of prose is already more than a digest
	maxLightAppSummary = 32 << 10 // structured extras, passed through verbatim
	maxLightAppImage   = 12 << 20 // a selection screenshot, before compression
)

// LightAppSnapshot is one push from a running Light App.
type LightAppSnapshot struct {
	Slug string
	// Digest is the app's own one-line description of its state. The app
	// knows what is inside it far better than the host does, so the host
	// never tries to derive this.
	Digest string
	// Summary is opaque structured data the app chose to include. Relayed to
	// the model as JSON, never interpreted here.
	Summary json.RawMessage
	// Image is an optional screenshot, held in memory only — see PutLightApp.
	Image     []byte
	ImageType string
	UpdatedAt time.Time
}

// Stale reports whether the snapshot is old enough that the model should be
// told so.
func (s *LightAppSnapshot) Stale(now time.Time) bool {
	return now.Sub(s.UpdatedAt) > lightAppStaleAfter
}

var lightAppMirror struct {
	mu   sync.Mutex
	apps map[string]*LightAppSnapshot
}

// PutLightApp records the latest snapshot for one app, replacing any previous
// one. Oversized fields are truncated rather than rejected: a snapshot is a
// courtesy from the app, and half a digest still beats no digest.
//
// The screenshot is kept in memory and nowhere else. Most are replaced within
// seconds or die when the app closes, and octo's upload store is
// content-addressed with no deletion — writing every frame to it would leak
// disk forever. It reaches disk only if view_lightapp actually hands it to the
// model.
func PutLightApp(snap LightAppSnapshot) {
	if snap.Slug == "" {
		return
	}
	if len(snap.Digest) > maxLightAppDigest {
		// Cut on a rune boundary: a digest is prose, often not ASCII, and
		// slicing bytes would leave a half character for json.Marshal to
		// replace with U+FFFD.
		snap.Digest = strings.ToValidUTF8(snap.Digest[:maxLightAppDigest], "") + "…"
	}
	if len(snap.Summary) > maxLightAppSummary {
		snap.Summary = nil
	}
	if len(snap.Image) > maxLightAppImage {
		snap.Image = nil
		snap.ImageType = ""
	}
	if snap.UpdatedAt.IsZero() {
		snap.UpdatedAt = time.Now()
	}

	lightAppMirror.mu.Lock()
	defer lightAppMirror.mu.Unlock()
	if lightAppMirror.apps == nil {
		lightAppMirror.apps = map[string]*LightAppSnapshot{}
	}
	evictLightAppsLocked(time.Now())
	lightAppMirror.apps[snap.Slug] = &snap
}

// evictLightAppsLocked drops snapshots nobody has refreshed in a long time.
// Lazy rather than a timer: the mirror is only interesting when something
// touches it, and every path that reads or writes it already holds the lock.
func evictLightAppsLocked(now time.Time) {
	for slug, s := range lightAppMirror.apps {
		if now.Sub(s.UpdatedAt) > lightAppEvictAfter {
			delete(lightAppMirror.apps, slug)
		}
	}
}

// DropLightApp forgets an app — sent when its frame goes away, so the tools
// stop describing something nobody is looking at.
func DropLightApp(slug string) {
	lightAppMirror.mu.Lock()
	defer lightAppMirror.mu.Unlock()
	delete(lightAppMirror.apps, slug)
}

// LightAppDeliverer hands a file to a running Light App. The tools package
// cannot reach the browser — it cannot even import the server, which imports
// it — so the server installs this on start. Unset (a CLI session, a test),
// the insert tool says so rather than pretending it delivered.
type LightAppDeliverer func(slug, path, note string) error

var lightAppDeliver struct {
	mu sync.Mutex
	fn LightAppDeliverer
}

// SetLightAppDeliverer installs the delivery path. Called by the server.
func SetLightAppDeliverer(fn LightAppDeliverer) {
	lightAppDeliver.mu.Lock()
	defer lightAppDeliver.mu.Unlock()
	lightAppDeliver.fn = fn
}

func lightAppDelivererFn() LightAppDeliverer {
	lightAppDeliver.mu.Lock()
	defer lightAppDeliver.mu.Unlock()
	return lightAppDeliver.fn
}

// lightAppSnapshot returns a copy of one app's snapshot, or nil.
func lightAppSnapshot(slug string) *LightAppSnapshot {
	lightAppMirror.mu.Lock()
	defer lightAppMirror.mu.Unlock()
	evictLightAppsLocked(time.Now())
	s := lightAppMirror.apps[slug]
	if s == nil {
		return nil
	}
	cp := *s
	return &cp
}

// lightAppSnapshots returns every connected app, slug order.
func lightAppSnapshots() []*LightAppSnapshot {
	lightAppMirror.mu.Lock()
	defer lightAppMirror.mu.Unlock()
	evictLightAppsLocked(time.Now())
	out := make([]*LightAppSnapshot, 0, len(lightAppMirror.apps))
	for _, s := range lightAppMirror.apps {
		cp := *s
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
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

// renderedSummaryMax is what one app may contribute to a lightapp_state
// answer. The ingest cap (maxLightAppSummary) is about what the mirror will
// hold; this is about what a tool the model is told to call freely may spend
// of its context.
const renderedSummaryMax = 512
