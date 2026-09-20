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

// Signature identifies the *content* of a snapshot, not just its shape. Two
// different selections that happen to have the same size must not share one,
// or a stale screenshot survives a selection swap.
func (s *LightAppSnapshot) Signature() string {
	return fmt.Sprintf("%s|%d|%d", s.Digest, len(s.Summary), len(s.Image))
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
		snap.Digest = snap.Digest[:maxLightAppDigest] + "…"
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
	lightAppMirror.apps[snap.Slug] = &snap
}

// DropLightApp forgets an app — sent when its frame goes away, so the tools
// stop describing something nobody is looking at.
func DropLightApp(slug string) {
	lightAppMirror.mu.Lock()
	defer lightAppMirror.mu.Unlock()
	delete(lightAppMirror.apps, slug)
}

// lightAppSnapshot returns a copy of one app's snapshot, or nil.
func lightAppSnapshot(slug string) *LightAppSnapshot {
	lightAppMirror.mu.Lock()
	defer lightAppMirror.mu.Unlock()
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

// renderLightAppLine is one app's line in the lightapp_state answer.
func renderLightAppLine(s *LightAppSnapshot, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- %s — updated %s", s.Slug, describeAge(now.Sub(s.UpdatedAt)))
	if s.Stale(now) {
		b.WriteString(" (stale: the user may have moved on)")
	}
	if len(s.Image) > 0 {
		b.WriteString(", has a screenshot you can view")
	}
	b.WriteString("\n")
	if s.Digest != "" {
		fmt.Fprintf(&b, "  %s\n", s.Digest)
	}
	if len(s.Summary) > 0 {
		fmt.Fprintf(&b, "  details: %s\n", string(s.Summary))
	}
	return b.String()
}
