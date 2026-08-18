package main

import (
	"strings"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// TestShellURL pins the shell-marker query the frontend keys nativeShell off.
// The string is duplicated across the Go/JS boundary (see desktopShellQuery and
// web/src/components/layout/VersionBadge.svelte); renaming one side without the
// other silently downgrades the desktop shell to a plain-web client — the OS
// file dialog and native header quietly stop working. This test locks the Go
// side of that contract.
func TestShellURL(t *testing.T) {
	const base = "http://127.0.0.1:8088"

	tests := []struct {
		name string
		hash string
		want string
	}{
		{"no hash", "", base + "/?shell=octo-desktop"},
		{"with hash", "settings", base + "/?shell=octo-desktop#settings"},
		{"nested route", "chat/abc123", base + "/?shell=octo-desktop#chat/abc123"},
	}
	for _, tt := range tests {
		if got := shellURL(base, tt.hash); got != tt.want {
			t.Errorf("shellURL(%q, %q) = %q, want %q", base, tt.hash, got, tt.want)
		}
	}

	// The marker must survive on every variant — it is the sole nativeShell signal.
	if got := shellURL(base, "x"); !strings.Contains(got, "shell=octo-desktop") {
		t.Fatalf("shellURL dropped the desktop marker: %q", got)
	}
}

// TestNeedsRevive is the false-positive guard for the whole feature: destroying
// a healthy window (losing the user's draft and stealing focus) is far worse
// than a few more seconds of black, so every legitimate reason a page has to be
// quiet must read as healthy. Only post-show evidence counts — how long the
// page was silent BEFORE the show is deliberately irrelevant, because system
// sleep freezes the page wholesale and Chromium throttles a long-hidden page's
// timers to roughly once a minute.
func TestNeedsRevive(t *testing.T) {
	shownAt := time.Now()
	cases := []struct {
		name       string
		frameAt    time.Time // zero = never
		hiddenAt   time.Time // zero = never
		wantRevive bool
	}{
		{"frame after the show: compositor is painting", shownAt.Add(time.Second), time.Time{}, false},
		{"hidden beat after the show: owes no frames", time.Time{}, shownAt.Add(time.Second), false},
		{"both after the show", shownAt.Add(time.Second), shownAt.Add(2 * time.Second), false},
		{"nothing at all: dead renderer", time.Time{}, time.Time{}, true},
		{"only frames from before the show: black compositor", shownAt.Add(-time.Minute), time.Time{}, true},
		{"only a hidden beat from before the show", time.Time{}, shownAt.Add(-time.Minute), true},
	}
	for _, tc := range cases {
		b := &nativeBridge{}
		if !tc.frameAt.IsZero() {
			b.lastFrame.Store(tc.frameAt.UnixNano())
		}
		if !tc.hiddenAt.IsZero() {
			b.lastHiddenBeat.Store(tc.hiddenAt.UnixNano())
		}
		if got := b.needsRevive(shownAt); got != tc.wantRevive {
			t.Errorf("%s: needsRevive = %v, want %v", tc.name, got, tc.wantRevive)
		}
	}
}

// TestNeedsReviveIgnoresPreShowSilence pins the sleep/wake case explicitly: a
// page frozen for hours (so every timestamp is ancient) that reports in right
// after the show is healthy. Judging the pre-show gap instead would destroy
// this window.
func TestNeedsReviveIgnoresPreShowSilence(t *testing.T) {
	shownAt := time.Now()
	b := &nativeBridge{}
	b.lastBeat.Store(shownAt.Add(-8 * time.Hour).UnixNano()) // asleep all night
	b.lastFrame.Store(shownAt.Add(-8 * time.Hour).UnixNano())
	if !b.needsRevive(shownAt) {
		t.Fatal("precondition: with only pre-show evidence the verdict is still revive")
	}
	// The page wakes and paints a frame after the show.
	b.lastFrame.Store(shownAt.Add(200 * time.Millisecond).UnixNano())
	if b.needsRevive(shownAt) {
		t.Error("a page that painted after the show must never be revived, however long it slept")
	}
}

// TestReviveAllowed pins the destroy/create rate limit: a first revive is
// always allowed, a second inside the cooldown is not — a persistently broken
// page must not put the shell into a reload loop.
func TestReviveAllowed(t *testing.T) {
	now := time.Now()
	b := &nativeBridge{}
	if !b.reviveAllowed(now) {
		t.Error("first revive should be allowed")
	}
	b.lastRevive.Store(now.Add(-reviveCooldown / 2).UnixNano())
	if b.reviveAllowed(now) {
		t.Error("revive inside the cooldown should be blocked")
	}
	b.lastRevive.Store(now.Add(-2 * reviveCooldown).UnixNano())
	if !b.reviveAllowed(now) {
		t.Error("revive after the cooldown should be allowed")
	}
}

// TestHeartbeatSignals pins the three things a beat can say, including the two
// ways a stale or hostile report must NOT erase good evidence: lastFrame only
// moves forward, and an absurd frame age is clamped rather than trusted (any
// local process can reach this endpoint on loopback).
func TestHeartbeatSignals(t *testing.T) {
	b := &nativeBridge{}

	// Visible beat: records JS liveness and a frame time derived from the age.
	before := time.Now()
	b.Heartbeat(3000)
	after := time.Now()
	if beat := time.Unix(0, b.lastBeat.Load()); beat.Before(before) || beat.After(after) {
		t.Errorf("lastBeat = %v, want within [%v, %v]", beat, before, after)
	}
	frame := time.Unix(0, b.lastFrame.Load())
	if frame.Before(before.Add(-3*time.Second)) || frame.After(after.Add(-3*time.Second)) {
		t.Errorf("lastFrame = %v, want ~3s before the beat", frame)
	}
	if b.lastHiddenBeat.Load() != 0 {
		t.Error("a visible beat must not record a hidden beat")
	}

	// Hidden beat (negative age): records liveness + "owes no frames", and
	// leaves the frame evidence untouched.
	frameBefore := b.lastFrame.Load()
	b.Heartbeat(-1)
	if b.lastHiddenBeat.Load() == 0 {
		t.Error("a hidden beat must be recorded so the probe expects no frames")
	}
	if b.lastFrame.Load() != frameBefore {
		t.Error("a hidden beat must not touch lastFrame")
	}

	// Monotonic: a stale report (a focus beat whose rAF has not ticked yet,
	// clock skew) must not move lastFrame backwards.
	b.Heartbeat(0) // fresh frame, now
	newest := b.lastFrame.Load()
	b.Heartbeat(int64(time.Minute / time.Millisecond)) // claims a minute-old frame
	if b.lastFrame.Load() != newest {
		t.Error("a staler frame report must not overwrite newer frame evidence")
	}

	// Clamp: an absurd age can't push lastFrame into the distant past (which,
	// with the monotonic rule, is the only way to fake a stall).
	b2 := &nativeBridge{}
	b2.Heartbeat(1 << 62)
	if age := time.Since(time.Unix(0, b2.lastFrame.Load())); age > maxReportedFrameAge+time.Minute {
		t.Errorf("frame age clamp failed: lastFrame is %v old", age)
	}
}

// TestProbeConstants pins the relationship the protocol depends on: the probe
// must outlast at least two scheduled beats, so a page that came back has
// certainly reported in before any verdict. A future tweak to one constant
// that breaks this would otherwise silently start destroying healthy windows.
func TestProbeConstants(t *testing.T) {
	if frameProbeDelay <= 2*heartbeatInterval {
		t.Errorf("frameProbeDelay (%v) must exceed two beats (%v)", frameProbeDelay, 2*heartbeatInterval)
	}
	if reviveCooldown <= frameProbeDelay {
		t.Errorf("reviveCooldown (%v) must exceed one probe cycle (%v)", reviveCooldown, frameProbeDelay)
	}
}

// TestDetachStalledGuardsWindowIdentity pins the revive swap's core invariant:
// the pointer is detached BEFORE the old window is closed (so the replacement
// survives the old one's WindowClosing handler), and the detach only fires for
// the exact window the probe judged — never for a successor the user or another
// revive installed meanwhile.
func TestDetachStalledGuardsWindowIdentity(t *testing.T) {
	now := time.Now()
	first := &application.WebviewWindow{}  // identity tokens only: no method is
	second := &application.WebviewWindow{} // called on them in this path

	b := &nativeBridge{window: second}
	if got := b.detachStalled(first, now); got != nil {
		t.Error("detachStalled must not touch a different (successor) window")
	}
	if b.window != second {
		t.Error("successor window must stay attached")
	}
	if got := b.detachStalled(second, now); got != second {
		t.Errorf("detachStalled = %v, want the probed window", got)
	}
	if b.window != nil {
		t.Error("detachStalled must clear b.window before the caller closes it")
	}

	// Cooldown blocks the detach entirely.
	b = &nativeBridge{window: first}
	b.lastRevive.Store(now.Add(-reviveCooldown / 2).UnixNano())
	if got := b.detachStalled(first, now); got != nil {
		t.Error("detach inside the cooldown must not fire")
	}
	if b.window != first {
		t.Error("window must stay attached when the cooldown blocks the revive")
	}
}

// TestProbeAfterShowHealthyPathAndFlag covers the probe goroutine's two
// bookkeeping duties: a healthy page short-circuits without touching the
// window, and probeInFlight is always released — a leak there would silently
// disable every future probe.
func TestProbeAfterShowHealthyPathAndFlag(t *testing.T) {
	win := &application.WebviewWindow{} // identity token; the healthy path calls nothing on it
	b := &nativeBridge{window: win, testProbeDelay: 10 * time.Millisecond}

	// Nil window: no probe, no flag left set.
	b.probeAfterShow(nil)
	if b.probeInFlight.Load() {
		t.Error("probeInFlight must not stay set when there is no window to probe")
	}

	// Healthy: a frame lands after the show, so the probe must leave the window
	// attached and clear its flag.
	b.lastFrame.Store(time.Now().Add(time.Second).UnixNano())
	b.probeAfterShow(win)
	deadline := time.Now().Add(2 * time.Second)
	for b.probeInFlight.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if b.probeInFlight.Load() {
		t.Fatal("probeInFlight leaked: future probes would never run")
	}
	if b.window != win {
		t.Error("a healthy window must not be detached by the probe")
	}
	if b.lastRevive.Load() != 0 {
		t.Error("a healthy probe must not consume the revive budget")
	}
}
