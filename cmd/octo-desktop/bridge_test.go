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

// TestWebviewDead pins the shell's revive decision: a window is declared dead
// only when it is old enough to have beaten AND the beats stopped (or never
// started). Young windows and actively-beating pages must never be revived —
// a false positive here destroys a healthy window.
func TestWebviewDead(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name    string
		shownAt time.Time // zero = no window recorded
		beatAt  time.Time // zero = never beat
		want    bool
	}{
		{"no window recorded", time.Time{}, time.Time{}, false},
		{"young window, no beat yet (still loading)", now.Add(-10 * time.Second), time.Time{}, false},
		{"old window, never beat", now.Add(-2 * beatGrace), time.Time{}, true},
		{"old window, fresh beat", now.Add(-time.Hour), now.Add(-2 * time.Second), false},
		{"old window, beats stopped", now.Add(-time.Hour), now.Add(-2 * beatGrace), true},
		{"beat predates this window (belongs to a previous one)", now.Add(-2 * beatGrace), now.Add(-3 * beatGrace), true},
	}
	for _, tc := range cases {
		b := &nativeBridge{}
		if !tc.shownAt.IsZero() {
			b.windowShownAt.Store(tc.shownAt.UnixNano())
		}
		if !tc.beatAt.IsZero() {
			b.lastBeat.Store(tc.beatAt.UnixNano())
		}
		if got := b.webviewDead(now); got != tc.want {
			t.Errorf("%s: webviewDead = %v, want %v", tc.name, got, tc.want)
		}
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

// TestHeartbeatRecordsFrameTime pins the frame-timestamp derivation the
// post-show probe depends on: lastFrame = beat arrival minus the reported rAF
// age, and a negative age (clock skew, bad client) must not move lastFrame
// into the future.
func TestHeartbeatRecordsFrameTime(t *testing.T) {
	b := &nativeBridge{}
	before := time.Now()
	b.Heartbeat(3000)
	after := time.Now()

	beat := time.Unix(0, b.lastBeat.Load())
	if beat.Before(before) || beat.After(after) {
		t.Errorf("lastBeat = %v, want within [%v, %v]", beat, before, after)
	}
	frame := time.Unix(0, b.lastFrame.Load())
	wantLo, wantHi := before.Add(-3*time.Second), after.Add(-3*time.Second)
	if frame.Before(wantLo) || frame.After(wantHi) {
		t.Errorf("lastFrame = %v, want ~3s before the beat", frame)
	}

	prevFrame := b.lastFrame.Load()
	b.Heartbeat(-50)
	if b.lastFrame.Load() != prevFrame {
		t.Error("negative frame age must not update lastFrame")
	}
	if time.Unix(0, b.lastBeat.Load()).Before(after) {
		t.Error("beat with negative frame age must still record JS liveness")
	}
}

// TestDetachHelpersGuardWindowIdentity pins the revive swap's core invariant:
// the pointer is detached BEFORE the old window is closed (so the replacement
// survives the old window's WindowClosing handler), and a stalled-window
// detach only fires for the exact window the probe judged — never for a
// successor the user or another revive installed meanwhile.
func TestDetachHelpersGuardWindowIdentity(t *testing.T) {
	now := time.Now()
	first := &application.WebviewWindow{}  // identity tokens only: no method is
	second := &application.WebviewWindow{} // called on them in these paths
	markDead := func(b *nativeBridge) {
		b.windowShownAt.Store(now.Add(-2 * beatGrace).UnixNano())
		b.lastBeat.Store(0)
	}

	// detachIfDead hands back the dead window and clears the field, so the
	// caller closes a window the bridge no longer points at.
	b := &nativeBridge{window: first}
	markDead(b)
	if got := b.detachIfDead(now); got != first {
		t.Errorf("detachIfDead = %v, want the dead window", got)
	}
	if b.window != nil {
		t.Error("detachIfDead must clear b.window before the caller closes it")
	}

	// A live (beating) window is never detached.
	b = &nativeBridge{window: first}
	b.windowShownAt.Store(now.Add(-time.Hour).UnixNano())
	b.lastBeat.Store(now.Add(-time.Second).UnixNano())
	if got := b.detachIfDead(now); got != nil {
		t.Error("a beating window must not be detached")
	}
	if b.window != first {
		t.Error("a beating window must stay attached")
	}

	// Cooldown blocks a second revive.
	b = &nativeBridge{window: first}
	markDead(b)
	b.lastRevive.Store(now.Add(-reviveCooldown / 2).UnixNano())
	if got := b.detachIfDead(now); got != nil {
		t.Error("revive inside the cooldown must not detach")
	}

	// detachStalled is identity-keyed: a successor window is left alone.
	b = &nativeBridge{window: second}
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
}
