package main

import (
	"strings"
	"sync/atomic"
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

// TestJudgePage covers the feature's central decision, whose two failure
// directions are both expensive: a wrong "healthy" leaves the user staring at a
// black window forever, and a wrong "black" destroys a working one (losing the
// draft, stealing focus). Only post-show evidence counts — pre-show silence is
// legitimate, since system sleep freezes the page and Chromium throttles a
// long-hidden page's timers to roughly once a minute.
func TestJudgePage(t *testing.T) {
	shownAt := time.Now()
	before, after := shownAt.Add(-time.Minute), shownAt.Add(time.Second)
	cases := []struct {
		name     string
		frameAt  time.Time // zero = never
		hiddenAt time.Time // zero = never
		beatAt   time.Time // zero = never
		want     pageVerdict
	}{
		{"painted after the show", after, time.Time{}, after, pageHealthy},
		{"hidden after the show: owes no frames", time.Time{}, after, after, pageHealthy},
		{"beats, visible, never painted: black", time.Time{}, time.Time{}, after, pageBlack},
		{"beats, frames only from before the show: black", before, time.Time{}, after, pageBlack},
		{"beats, hidden only from before the show: black", time.Time{}, before, after, pageBlack},
		{"nothing at all since the show", time.Time{}, time.Time{}, time.Time{}, pageSilent},
		{"only pre-show evidence of everything", before, before, before, pageSilent},
	}
	for _, tc := range cases {
		b := &nativeBridge{}
		if !tc.frameAt.IsZero() {
			b.lastFrame.Store(tc.frameAt.UnixNano())
		}
		if !tc.hiddenAt.IsZero() {
			b.lastHiddenBeat.Store(tc.hiddenAt.UnixNano())
		}
		if !tc.beatAt.IsZero() {
			b.lastBeat.Store(tc.beatAt.UnixNano())
		}
		if got := b.judgePage(shownAt); got != tc.want {
			t.Errorf("%s: judgePage = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestJudgePageBlackFromBirth guards a hole an earlier cut of this protocol
// had: reporting "hidden" and "no frame observed yet" through a single signal
// made a window that was black from its first paint look healthy forever — and
// with it a replacement window that also came up black, which is precisely when
// the retry matters. A visible page that beats without ever painting is black.
func TestJudgePageBlackFromBirth(t *testing.T) {
	// Back off the show anchor: CI clocks (Windows ~0.5ms, virtualised macOS)
	// can return the same time.Now() value for the show and the beats, and
	// judgePage's After() is strict — equal timestamps would read as pre-show
	// evidence. A beat in the same clock tick as the show is a non-case in
	// production (beats arrive seconds later).
	shownAt := time.Now().Add(-time.Millisecond)

	b := &nativeBridge{}
	for i := 0; i < 5; i++ {
		b.Heartbeat(-1, false) // visible, rAF never fires
	}
	if got := b.judgePage(shownAt); got != pageBlack {
		t.Errorf("visible page that never painted: judgePage = %v, want pageBlack", got)
	}

	b2 := &nativeBridge{}
	for i := 0; i < 5; i++ {
		b2.Heartbeat(-1, true) // same beats, but hidden
	}
	if got := b2.judgePage(shownAt); got != pageHealthy {
		t.Errorf("hidden page: judgePage = %v, want pageHealthy", got)
	}
}

// TestJudgePageIgnoresPreShowSilence pins the sleep/wake case explicitly: a page
// frozen for hours (so every timestamp is ancient) that reports in right after
// the show is healthy. Judging the pre-show gap instead would destroy it.
func TestJudgePageIgnoresPreShowSilence(t *testing.T) {
	shownAt := time.Now()
	b := &nativeBridge{}
	b.lastBeat.Store(shownAt.Add(-8 * time.Hour).UnixNano()) // asleep all night
	b.lastFrame.Store(shownAt.Add(-8 * time.Hour).UnixNano())
	if got := b.judgePage(shownAt); got != pageSilent {
		t.Fatalf("precondition: judgePage = %v, want pageSilent with only pre-show evidence", got)
	}
	// The page wakes and paints after the show.
	b.lastBeat.Store(shownAt.Add(200 * time.Millisecond).UnixNano())
	b.lastFrame.Store(shownAt.Add(200 * time.Millisecond).UnixNano())
	if got := b.judgePage(shownAt); got != pageHealthy {
		t.Errorf("judgePage = %v, want pageHealthy: painting after the show clears it however long it slept", got)
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
	b.Heartbeat(3000, false)
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

	// Hidden beat: records liveness + "owes no frames", and leaves the frame
	// evidence untouched.
	frameBefore := b.lastFrame.Load()
	b.Heartbeat(-1, true)
	if b.lastHiddenBeat.Load() == 0 {
		t.Error("a hidden beat must be recorded so the probe expects no frames")
	}
	if b.lastFrame.Load() != frameBefore {
		t.Error("a hidden beat must not touch lastFrame")
	}

	// Visible with no frame yet: liveness only. It must NOT be filed as
	// "owes no frames" — that is the black-window signature.
	hiddenBefore := b.lastHiddenBeat.Load()
	b.Heartbeat(-1, false)
	if b.lastHiddenBeat.Load() != hiddenBefore {
		t.Error("a visible never-painted beat must not count as hidden")
	}
	if b.lastFrame.Load() != frameBefore {
		t.Error("a visible never-painted beat must not invent frame evidence")
	}

	// Monotonic: a stale report (a focus beat whose rAF has not ticked yet,
	// clock skew) must not move lastFrame backwards.
	b.Heartbeat(0, false) // fresh frame, now
	newest := b.lastFrame.Load()
	b.Heartbeat(int64(time.Minute/time.Millisecond), false) // claims a minute-old frame
	if b.lastFrame.Load() != newest {
		t.Error("a staler frame report must not overwrite newer frame evidence")
	}

	// Clamp: an absurd age can't push lastFrame into the distant past (which,
	// with the monotonic rule, is the only way to fake a stall).
	b2 := &nativeBridge{}
	b2.Heartbeat(1<<62, false)
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

// TestProbeAfterShowRevivesBlackWindow covers the branch that actually repairs
// a black window, through the reviveFn seam (the real one needs a live Wails
// app). It pins two things the shell's own survival depends on: the window is
// detached from the bridge BEFORE it is handed over for closing, and the revive
// budget is consumed so a still-broken page can't loop.
func TestProbeAfterShowRevivesBlackWindow(t *testing.T) {
	win := &application.WebviewWindow{}
	got := make(chan *application.WebviewWindow, 1)
	var detachedAtHandover bool

	b := &nativeBridge{window: win, testProbeDelay: 10 * time.Millisecond}
	b.reviveFn = func(old *application.WebviewWindow) {
		b.windowMu.Lock()
		detachedAtHandover = b.window == nil
		b.windowMu.Unlock()
		got <- old
	}

	// Beats arrive, the page claims to be visible, no frame ever lands: black.
	b.Heartbeat(-1, false)
	b.probeAfterShow(win)

	select {
	case old := <-got:
		if old != win {
			t.Errorf("revive got %v, want the probed window", old)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a black window was never revived")
	}
	if !detachedAtHandover {
		t.Error("the window must be detached before it is handed over to be closed, or its close would nil the replacement")
	}
	if b.lastRevive.Load() == 0 {
		t.Error("a revive must consume the budget so a persistently broken page can't loop")
	}
}

// TestProbeAfterShowGivesSilenceASecondChance pins the deliberate asymmetry
// between the two unhealthy verdicts: total silence can also mean a wedged hub
// or a main thread stuck on a huge render, both of which recover on their own,
// so the window is only replaced if the silence persists. A page that reports in
// during the second cycle keeps its state.
func TestProbeAfterShowGivesSilenceASecondChance(t *testing.T) {
	win := &application.WebviewWindow{}
	revived := make(chan struct{}, 1)
	b := &nativeBridge{window: win, testProbeDelay: 30 * time.Millisecond}
	b.reviveFn = func(*application.WebviewWindow) { revived <- struct{}{} }

	b.probeAfterShow(win) // no beats at all → pageSilent
	// Report in before the second cycle elapses.
	time.Sleep(40 * time.Millisecond)
	b.Heartbeat(0, false)

	select {
	case <-revived:
		t.Error("a page that reported in during the second chance must not be revived")
	case <-time.After(300 * time.Millisecond):
	}
	if b.window != win {
		t.Error("window must stay attached")
	}
}

// TestProbeAfterShowCollapsesConcurrentProbes pins the probeInFlight CAS: a
// second probe requested while one is still running must be skipped, not
// queued — two overlapping probes would judge overlapping windows of evidence
// and could both reach for the window. The test parks the first probe inside
// the revive hand-off, then asks for another probe against a fresh window
// whose evidence would revive it if a second goroutine were actually running
// (the revive cooldown is reset so it couldn't mask the attempt).
func TestProbeAfterShowCollapsesConcurrentProbes(t *testing.T) {
	first := &application.WebviewWindow{}
	second := &application.WebviewWindow{}
	release := make(chan struct{})
	var revives atomic.Int32

	b := &nativeBridge{window: first, testProbeDelay: 10 * time.Millisecond}
	b.reviveFn = func(*application.WebviewWindow) {
		revives.Add(1)
		<-release // park the first probe with probeInFlight still held
	}

	b.probeAfterShow(first)
	// Evidence must land AFTER the show to count: visible, never painted → black.
	b.Heartbeat(-1, false)

	// Wait until the first probe is parked inside the revive hand-off.
	deadline := time.Now().Add(2 * time.Second)
	for revives.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if revives.Load() != 1 {
		t.Fatal("first probe never reached the revive")
	}
	if !b.probeInFlight.Load() {
		t.Fatal("probeInFlight must stay held while the first probe runs")
	}

	// A second show during the in-flight probe: its window has black evidence
	// and (with the cooldown reset) would be revived IF a second probe
	// goroutine were scheduled. The CAS must collapse it instead.
	b.windowMu.Lock()
	b.window = second
	b.windowMu.Unlock()
	b.lastRevive.Store(0)
	b.probeAfterShow(second)
	b.Heartbeat(-1, false) // black evidence for the second window, after its show

	close(release) // let the first probe finish
	time.Sleep(100 * time.Millisecond)
	if n := revives.Load(); n != 1 {
		t.Errorf("revives = %d, want 1 — the second probe was not collapsed", n)
	}
	if b.probeInFlight.Load() {
		t.Error("probeInFlight leaked after the first probe finished")
	}
	b.windowMu.Lock()
	if b.window != second {
		t.Error("the collapsed probe must not touch the successor window")
	}
	b.windowMu.Unlock()
}

// TestCloseShouldQuit pins who owns the last-window-close quit on each
// platform. Windows' framework-level quit is suppressed because it fired
// unconditionally — killing a hub the user had asked to keep running — so the
// close path must perform the quit itself, and ONLY when the user opted out of
// background running. mac and Linux already route this through their own hooks
// and must never get a second quit from here.
func TestCloseShouldQuit(t *testing.T) {
	cases := []struct {
		goos      string
		allowQuit bool
		want      bool
	}{
		{"windows", true, true},   // user opted out of background running
		{"windows", false, false}, // keep running in the tray
		{"darwin", true, false},   // ApplicationShouldTerminateAfterLastWindowClosed owns it
		{"darwin", false, false},
		{"linux", true, false}, // unregisterWindow consults ShouldQuit
		{"linux", false, false},
	}
	for _, tc := range cases {
		if got := closeShouldQuit(tc.goos, tc.allowQuit); got != tc.want {
			t.Errorf("closeShouldQuit(%q, allowQuit=%v) = %v, want %v", tc.goos, tc.allowQuit, got, tc.want)
		}
	}
}
