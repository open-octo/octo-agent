package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/open-octo/octo-agent/internal/server"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// nativeBridge implements server.NativeBridge on top of the Wails runtime. The
// server calls it (from HTTP-handler goroutines) for the capabilities a
// browser can't provide; Wails' own APIs marshal to the UI thread internally,
// so no manual main-thread dispatch is needed here.
type nativeBridge struct {
	app      *application.App
	window   *application.WebviewWindow
	notifier *notifications.NotificationService // nil unless bundled
	// srv is the in-process hub, set once bound. Atomic because startHub (the
	// ApplicationStarted goroutine) writes it while the tray-refresh loop reads it.
	srv atomic.Pointer[server.Server]
	url string // http://127.0.0.1:8088, set once bound

	// closeLog releases the rotating serve.log writer that startHub installs once
	// this process owns the port. Written by startHub (the ApplicationStarted
	// goroutine) and read by main's post-Run cleanup on another goroutine, so it's
	// atomic like srv.
	closeLog atomic.Pointer[func()]

	// allowQuit gates the app's ShouldQuit on Windows/Linux, where closing the
	// last window would otherwise terminate the app (and the hub with it). It
	// starts false when KeepRunningInBackground is on, so a window close hides
	// to the tray; requestQuit flips it true for a real quit. Unused on macOS
	// (its close behavior is the ApplicationShouldTerminateAfterLastWindowClosed
	// option; ShouldQuit there always allows the quit).
	allowQuit atomic.Bool

	// Webview liveness, fed by POST /api/native/heartbeat from the page in
	// nativeShell mode (see startNativeHeartbeat in the frontend). All unix
	// nanos, atomic: HTTP-handler goroutines write, the show/probe paths read.
	// lastBeat is when a beat last arrived (JS provably alive then).
	// lastFrame is when the page last observed a requestAnimationFrame tick
	// (the render pipeline provably alive then). lastHiddenBeat is when the
	// page last reported itself hidden — it owes no frames then, so a missing
	// frame must not read as a black window. windowShownAt anchors every
	// verdict to the last show, probeInFlight collapses concurrent probes, and
	// lastRevive rate-limits re-creation.
	lastBeat       atomic.Int64
	lastFrame      atomic.Int64
	lastHiddenBeat atomic.Int64
	windowShownAt  atomic.Int64
	probeInFlight  atomic.Bool
	lastRevive     atomic.Int64

	// testProbeDelay shortens probeAfterShow's sleep in tests; zero means the
	// production frameProbeDelay.
	testProbeDelay time.Duration

	// windowMu guards every read and write of b.window that could race the
	// pointer being REPLACED: showWindowAt's snapshot, the liveness probe's
	// background goroutine, and the WindowClosing handler that clears it. Held
	// only around the pointer access, never across a window method — those
	// marshal to the UI thread and would deadlock under a lock. The read-only
	// accessors (Minimise, Close, WindowState…) stay lock-free as they were.
	windowMu sync.Mutex

	// notifySeq makes each Notify call's notification identifier unique. macOS
	// rejects an addNotificationRequest with an empty identifier (its completion
	// handler errors and nothing is delivered), and a shared constant id would
	// make each new toast silently replace the previous one, so every call gets
	// its own.
	notifySeq atomic.Uint64

	// updateAvailable holds the version string of a newer release once an update
	// check (manual or the periodic auto-check) finds one, or nil when up to
	// date. The tray menu reads it to show a persistent, clickable "download"
	// item instead of relying on the transient system notification, which macOS
	// suppresses while the app is foreground — exactly when a manual check runs.
	// Atomic: the auto-check goroutine writes it while the tray loop / UI thread
	// read it.
	updateAvailable atomic.Pointer[string]
	// inplaceUpdate reports whether this build swaps itself via app.Updater
	// (bundled release build; see canInplaceUpdate) — when false, update
	// actions open the download page instead. Written once in main before the
	// event loop starts; atomic because tray/notification callbacks read it
	// from other goroutines.
	inplaceUpdate atomic.Bool
	// updateFlowBusy single-flights startUpdateFlow across its entry points
	// (tray item, toast tap, web badge): a second concurrent CheckAndInstall
	// would tear down the running flow's window and misreport
	// ErrDownloadInProgress as a failure.
	updateFlowBusy atomic.Bool
	// updateRestart is set when the user confirms the updater window's
	// restart: the quit that follows must never be vetoed (the updater's
	// helper waits for this process to exit before swapping the binary, so a
	// veto deadlocks the update). Sticky by design — it flips only on the
	// restart hand-off, after which the process is exiting anyway.
	updateRestart atomic.Bool
	// tray is the system-tray handle, stored so an update check can refresh the
	// menu immediately rather than waiting for refreshTrayLoop's next tick.
	tray atomic.Pointer[application.SystemTray]

	settingsMu sync.Mutex
	settings   desktopSettings
	// geomTimer debounces persistence of the window geometry to disk: a drag
	// fires WindowDidResize once per pixel, so we coalesce to a single write
	// ~400ms after the gesture settles. Guarded by settingsMu.
	geomTimer *time.Timer
}

// Built-in window size for a first launch (no saved geometry yet).
const (
	defaultWindowWidth  = 1280
	defaultWindowHeight = 860
)

// desktopShellQuery marks the window's URL so the frontend can tell it is
// running inside the desktop-shell webview rather than an external browser
// pointed at the same hub. The hub reports native=true to every client (the
// NativeBridge is a server-wide capability), but only the shell webview should
// behave as "native" — use the OS file dialog, route notifications through the
// OS, inset the header past the traffic lights. An external browser on this
// machine keys off the absent marker and stays plain web. The frontend reads it
// from location.search (see VersionBadge.svelte).
const desktopShellQuery = "shell=octo-desktop"

// shellURL builds the desktop-shell window URL for a frontend route hash,
// always carrying the desktopShellQuery marker. base is b.url, e.g.
// "http://127.0.0.1:8088". Fresh-window loads and SetURL navigations share it,
// so they produce the identical path+query and a route change stays a pure
// hashchange (no reload). The exact query string is contracted with the
// frontend reader isDesktopShell in web/src/lib/stores.ts — keep both sides in
// sync (TestShellURL pins the Go side).
func shellURL(base, hash string) string {
	u := base + "/?" + desktopShellQuery
	if hash != "" {
		u += "#" + hash
	}
	return u
}

// rememberWindowGeometry captures the window's size and maximised state into
// settings and debounces the disk write. The window is read HERE, from the
// WindowDidResize handler where it is guaranteed alive — never from the debounce
// timer or the close path. Those paths run on their own goroutines and would
// race the window's destruction: Wails' built-in WindowClosing listener marks
// the window destroyed, after which IsMaximised()/Size() short-circuit to
// false/0×0 and would clobber the freshly-saved state. The window methods are
// read before taking the lock (they marshal to the UI thread; holding
// settingsMu across that could deadlock the main thread). Size is recorded only
// while neither maximised, fullscreen, nor minimised — maximised/fullscreen
// report a size that isn't the windowed size, and on Windows a minimised window
// reports its taskbar-button size (≈160×28); persisting any of these would
// corrupt the restore size a relaunch un-maximises back to.
func (b *nativeBridge) rememberWindowGeometry(w *application.WebviewWindow) {
	maximised := w.IsMaximised()
	fullscreen := w.IsFullscreen()
	minimised := w.IsMinimised()
	width, height := w.Size()

	b.settingsMu.Lock()
	defer b.settingsMu.Unlock()
	b.settings.WindowMaximised = maximised
	if !maximised && !fullscreen && !minimised && width > 0 && height > 0 {
		b.settings.WindowWidth = width
		b.settings.WindowHeight = height
	}
	if b.geomTimer != nil {
		b.geomTimer.Stop()
	}
	b.geomTimer = time.AfterFunc(400*time.Millisecond, b.persistSettings)
}

// persistSettings writes the current settings to disk. It touches no window
// state, so it is safe to call from the debounce timer or the closing window.
func (b *nativeBridge) persistSettings() {
	b.settingsMu.Lock()
	snapshot := b.settings
	b.settingsMu.Unlock()
	_ = saveDesktopSettings(snapshot)
}

// PickFolder opens the OS directory-choose dialog and returns the chosen path.
// PromptForSingleSelection returns "" when the user cancels, which we surface
// as cancelled=true so the caller leaves the working dir untouched.
func (b *nativeBridge) PickFolder(_ context.Context, startDir string) (string, bool, error) {
	dlg := b.app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true)
	// Attach to the window so it opens as a sheet. A detached panel (Wails'
	// beginWithCompletionHandler path when no window is set) stops responding
	// to its buttons after the user switches away from the app and back.
	if b.window != nil {
		dlg.AttachToWindow(b.window)
	}
	if startDir != "" {
		dlg.SetDirectory(startDir)
	}
	path, err := dlg.PromptForSingleSelection()
	if err != nil {
		return "", false, err
	}
	if path == "" {
		return "", true, nil
	}
	return path, false, nil
}

// PickFile opens the OS file-choose dialog and returns the chosen path,
// cancelled when dismissed.
func (b *nativeBridge) PickFile(_ context.Context, startDir string) (string, bool, error) {
	dlg := b.app.Dialog.OpenFile().
		CanChooseFiles(true).
		CanChooseDirectories(false)
	if b.window != nil { // sheet, not a detached panel — see PickFolder.
		dlg.AttachToWindow(b.window)
	}
	if startDir != "" {
		dlg.SetDirectory(startDir)
	}
	path, err := dlg.PromptForSingleSelection()
	if err != nil {
		return "", false, err
	}
	if path == "" {
		return "", true, nil
	}
	return path, false, nil
}

// SaveFile shows the OS save dialog seeded with defaultName, writes content to
// the chosen path, and returns it. cancelled when dismissed. This is how the
// artifact "Download" action lands a file on disk in the desktop shell — the
// octo-served page has no webview download delegate, so an in-page blob
// download does nothing.
func (b *nativeBridge) SaveFile(_ context.Context, defaultName, content string) (string, bool, error) {
	dlg := b.app.Dialog.SaveFile().CanCreateDirectories(true)
	if b.window != nil { // sheet, not a detached panel — see PickFolder.
		dlg.AttachToWindow(b.window)
	}
	if defaultName != "" {
		dlg.SetFilename(defaultName)
	}
	path, err := dlg.PromptForSingleSelection()
	if err != nil {
		return "", false, err
	}
	if path == "" {
		return "", true, nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", false, err
	}
	return path, false, nil
}

// Print opens the OS print dialog for the window's current content — how the
// transcript's PDF export lands a file, since the shell can't be relied on to
// honour an in-page window.print(). Wails prints natively on macOS and forwards
// to window.print() on Windows and Linux.
//
// Non-blocking: on macOS the print panel runs as a window sheet, so this
// returns while it is still open and the page must keep its print layout up.
// No-op before the window exists.
func (b *nativeBridge) Print() error {
	if b.window == nil {
		return nil
	}
	return b.window.Print()
}

// Notify raises an OS-native notification. No-op when the notifications service
// isn't available (an unbundled dev binary — the service needs a bundle id).
// Best-effort by contract: a delivery failure (e.g. permission not yet granted)
// is swallowed.
func (b *nativeBridge) Notify(title, body string) {
	if b.notifier == nil {
		return
	}
	_ = b.notifier.SendNotification(notifications.NotificationOptions{
		ID:    fmt.Sprintf("octo-notify-%d", b.notifySeq.Add(1)),
		Title: title,
		Body:  body,
	})
}

// NotifySession raises an OS-native notification that, when clicked, brings the
// user to the given session. The session ID travels in the notification's Data
// payload and is echoed back as UserInfo in the platform response handler.
func (b *nativeBridge) NotifySession(title, body, sessionID string) {
	if b.notifier == nil {
		return
	}
	_ = b.notifier.SendNotification(notifications.NotificationOptions{
		ID:    fmt.Sprintf("octo-notify-%d", b.notifySeq.Add(1)),
		Title: title,
		Body:  body,
		Data: map[string]any{
			"session_id": sessionID,
		},
	})
}

// requestNotificationAuthorization asks the OS for permission to post
// notifications, without which every Notify call silently no-ops. macOS
// requires this at runtime: UNUserNotificationCenter drops delivery (reporting
// no error) until the user has granted authorization, so the first call raises
// the system permission prompt and blocks until they answer — run it off the UI
// thread. Windows/Linux grant immediately. No-op without a notifier.
func (b *nativeBridge) requestNotificationAuthorization() {
	if b.notifier == nil {
		return
	}
	_, _ = b.notifier.RequestNotificationAuthorization()
}

// Update-check notifications: the tray "Check for Updates…" flow reports via a
// toast rather than a modal dialog. The "update available" toast carries an
// action button; both it and a tap on the body start the update flow (in-place
// install, or the download page — see startUpdateFlow), routed in main.go's
// OnNotificationResponse handler by matching updateNotifyCategoryID.
const (
	updateNotifyID           = "octo-update-available"
	updateNotifyCategoryID   = "octo.update-available"
	updateNotifyOpenActionID = "octo.update-open"
)

// registerUpdateNotifyCategory registers the category that gives the "update
// available" toast its action button — "Update Now" when this build installs
// in place, "Open Download Page" otherwise. No-op when the notifier is
// unavailable (an unbundled dev binary). Register once at startup, after the
// notification service has started (and after main sets inplaceUpdate).
func (b *nativeBridge) registerUpdateNotifyCategory() {
	if b.notifier == nil {
		return
	}
	action := L().updOpen
	if b.inplaceUpdate.Load() {
		action = L().updInstall
	}
	_ = b.notifier.RegisterNotificationCategory(notifications.NotificationCategory{
		ID: updateNotifyCategoryID,
		Actions: []notifications.NotificationAction{
			{ID: updateNotifyOpenActionID, Title: action},
		},
	})
}

// NotifyUpdateAvailable raises the actionable "update available" toast. Its
// action button (and a tap on the body) open the download page via the
// OnNotificationResponse handler. Same best-effort contract as Notify: no-op
// when the notifier is unavailable.
func (b *nativeBridge) NotifyUpdateAvailable(title, body string) {
	if b.notifier == nil {
		return
	}
	_ = b.notifier.SendNotificationWithActions(notifications.NotificationOptions{
		ID:         updateNotifyID,
		Title:      title,
		Body:       body,
		CategoryID: updateNotifyCategoryID,
	})
}

// refreshTray rebuilds and re-publishes the tray menu right now, so an update
// check's result shows without waiting for refreshTrayLoop's next tick. No-op
// until the tray handle has been stored. SetMenu marshals to the UI thread, so
// it's safe to call from the check goroutine.
func (b *nativeBridge) refreshTray() {
	if t := b.tray.Load(); t != nil {
		t.SetMenu(buildTrayMenu(b.app, b))
	}
}

// AutostartEnabled reports whether the app is registered to launch at login.
func (b *nativeBridge) AutostartEnabled() (bool, error) {
	st, err := b.app.Autostart.Status()
	if err != nil {
		return false, err
	}
	return st.Enabled, nil
}

// SetAutostart registers or unregisters the app from launch-at-login.
func (b *nativeBridge) SetAutostart(enable bool) error {
	if enable {
		return b.app.Autostart.Enable()
	}
	return b.app.Autostart.Disable()
}

// ToggleMaximise maximises or restores the window (the double-click-titlebar
// zoom the frontend can't trigger itself). No-op before the window exists.
func (b *nativeBridge) ToggleMaximise() {
	if b.window != nil {
		b.window.ToggleMaximise()
	}
}

// Minimise minimises the window to the taskbar/dock. No-op before the window exists.
func (b *nativeBridge) Minimise() {
	if b.window != nil {
		b.window.Minimise()
	}
}

// Close closes the window (sends WindowClosing, after which the app's ShouldQuit
// decides whether the hub actually terminates or keeps running in the tray).
// No-op before the window exists.
func (b *nativeBridge) Close() {
	if b.window != nil {
		b.window.Close()
	}
}

// WindowState reports whether the window is currently maximised. Returns false
// before the window exists.
func (b *nativeBridge) WindowState() bool {
	if b.window == nil {
		return false
	}
	return b.window.IsMaximised()
}

// showWindow brings the hub window to the foreground on the current view.
func (b *nativeBridge) showWindow() { b.showWindowAt("") }

// openSettings brings the window up and opens the Settings modal (tray
// "Settings"). The frontend maps the "settings" hash route to the modal.
func (b *nativeBridge) openSettings() { b.showWindowAt("settings") }

// openNewSession brings the window up and starts a new session (tray
// "New Session" / a keyboard-⌘N-equivalent from the shell side). The frontend
// maps the "new" hash route to createNewSession(), the same action the in-app
// "New Session" button and Cmd/Ctrl+N trigger.
func (b *nativeBridge) openNewSession() { b.showWindowAt("new") }

// showWindowAt brings the hub window to the foreground, re-creating it if it was
// closed to the tray (KeepRunningInBackground), and navigates to the given
// frontend hash route (empty = leave it where it is). The frontend routes on
// location.hash, so a fresh window loads straight into the view and an existing
// one navigates via a hashchange — no full reload.
func (b *nativeBridge) showWindowAt(hash string) {
	// The marker rides on every navigation the shell performs (fresh window and
	// SetURL alike) so nativeShell stays true across reloads and route changes.
	target := shellURL(b.url, hash)
	// Snapshot the pointer once: the frame probe's goroutine can clear it
	// concurrently, and a lock-free re-read mid-function could see that nil and
	// panic. Everything below works off win, then publishes it back.
	b.windowMu.Lock()
	win := b.window
	b.windowMu.Unlock()
	created := false
	if win == nil {
		created = true
		if b.app == nil || b.url == "" {
			return // not bound yet
		}
		// Restore the size and maximised state saved from the last session.
		b.settingsMu.Lock()
		width, height, maximised := b.settings.WindowWidth, b.settings.WindowHeight, b.settings.WindowMaximised
		b.settingsMu.Unlock()
		if width <= 0 || height <= 0 {
			width, height = defaultWindowWidth, defaultWindowHeight
		}
		startState := application.WindowStateNormal
		if maximised {
			startState = application.WindowStateMaximised
		}
		w := b.app.Window.NewWithOptions(application.WebviewWindowOptions{
			Title:      "Octo",
			Width:      width,
			Height:     height,
			StartState: startState,
			URL:        target,
			// Mac keeps its native title bar (hidden, inset traffic lights)
			// instead of Frameless, so the real NSWindow buttons — and their
			// native hover/zoom/tiling-menu behaviour — render for free.
			// InvisibleTitleBarHeight is deliberately left unset: that hack
			// natively swallows every left-mouse-down in its strip (including
			// double-clicks) before the DOM ever sees them, which broke
			// double-click-to-zoom the first time a native title bar was
			// tried. Window dragging instead goes through the same JS gesture
			// detection (framelessDrag.ts) Windows/Linux use — it isn't
			// mac-exclusive, so double-clicks reach the DOM normally there.
			// Windows/Linux stay Frameless with the frontend's own controls.
			Frameless: runtime.GOOS != "darwin",
			Mac: application.MacWindow{
				TitleBar: application.MacTitleBarHiddenInset,
			},
		})
		// Forget the window when it closes so a later Show re-creates one; the
		// app itself stays alive via ApplicationShouldTerminateAfterLastWindowClosed.
		// Cancel any pending debounce and flush the last captured geometry — this
		// only persists already-captured settings, so unlike reading the window
		// here it can't race the window's destruction.
		w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
			b.settingsMu.Lock()
			if b.geomTimer != nil {
				b.geomTimer.Stop()
			}
			b.settingsMu.Unlock()
			b.persistSettings()
			// Guarded: when a revive replaced this window, b.window already
			// points at the successor and must survive the old one's close.
			b.windowMu.Lock()
			replaced := b.window != w
			if !replaced {
				b.window = nil
			}
			b.windowMu.Unlock()
			// Windows only: the framework's own last-window-close quit is
			// suppressed (see DisableQuitOnLastWindowClosed in main.go), so a
			// user who opted out of background running needs the quit performed
			// here. Never for a revive's discarded window — that close is the
			// shell repairing itself, not the user leaving.
			if !replaced && closeShouldQuit(runtime.GOOS, b.allowQuit.Load()) {
				b.app.Quit()
			}
		})
		// Capture size/maximised changes as the user drags or zooms the window.
		w.OnWindowEvent(events.Common.WindowDidResize, func(*application.WindowEvent) {
			b.rememberWindowGeometry(w)
		})
		// macOS kills WKWebView's WebContent process under memory pressure,
		// leaving a permanently blank window — nothing reloads it on its own.
		// Navigate back to the hub root to revive the page (SetURL for the
		// same reason as below: the page is octo-served, so the Wails runtime
		// and ExecJS are unavailable). Windows' equivalent (WebView2
		// ProcessFailed) isn't reachable through Wails — prevention lives in
		// the browser flags set in main.go instead. The b.window guard skips
		// a terminate that races window close: Wails' SetURL has no
		// isDestroyed check, so it could otherwise touch a freed NSWindow.
		if runtime.GOOS == "darwin" {
			w.OnWindowEvent(events.Mac.WebViewWebContentProcessDidTerminate, func(*application.WindowEvent) {
				b.windowMu.Lock()
				current := b.window
				b.windowMu.Unlock()
				if current != w {
					return
				}
				w.SetURL(shellURL(b.url, ""))
			})
		}
		win = w
		b.windowMu.Lock()
		b.window = w
		b.windowMu.Unlock()
	} else if hash != "" {
		// Already open — navigate to the route. ExecJS can't be used here: the
		// page is served by octo's own server, not Wails' asset server, so the
		// Wails runtime never loads and ExecJS stays queued forever. SetURL is a
		// native navigation that doesn't depend on it.
		win.SetURL(target)
	}
	win.Show()
	// Only un-minimise here. Wails' Restore() also un-maximises (and exits
	// fullscreen), so calling it unconditionally on every show/reopen — e.g.
	// clicking the dock icon to return to a maximised window — would shrink the
	// window back to its launch size. Guard on IsMinimised so a visible
	// maximised/fullscreen window keeps its size.
	if win.IsMinimised() {
		win.Restore()
	}
	win.Focus()
	// Every show is the anchor for the liveness verdict: a page only owes
	// evidence of life AFTER it was asked to come up. A freshly created window
	// is exempt — its first load can outlast the probe window, and if it never
	// comes up the next show judges it.
	b.windowShownAt.Store(time.Now().UnixNano())
	if !created {
		b.probeAfterShow(win)
	}
}

// Webview liveness timings. Every verdict is reached from evidence produced
// AFTER a show, never from how long the page was quiet before one: a page has
// many legitimate reasons to go silent in the background (system sleep freezes
// it wholesale, Chromium throttles a long-hidden page's timers to ~1/minute)
// and destroying a healthy window is far worse than a few more seconds of
// black. probeDelay therefore only has to outlast two scheduled beats, so a
// page that came back has certainly reported in.
const (
	heartbeatInterval   = 5 * time.Second // matches BEAT_MS in web/src/lib/nativeHeartbeat.ts
	frameProbeDelay     = 3 * heartbeatInterval
	reviveCooldown      = 90 * time.Second
	maxReportedFrameAge = 10 * time.Minute // clamp: a bad/hostile beat must not move lastFrame far
)

// closeShouldQuit reports whether closing the window must terminate the app.
// Only Windows needs this: mac routes last-window-close through
// ApplicationShouldTerminateAfterLastWindowClosed and Linux's
// unregisterWindow consults ShouldQuit, but the Windows backend's own quit is
// suppressed (DisableQuitOnLastWindowClosed) because it fired unconditionally
// — killing a hub the user wanted kept alive. allowQuit is false exactly when
// the user has KeepRunningInBackground on, so closing hides to the tray.
func closeShouldQuit(goos string, allowQuit bool) bool {
	return goos == "windows" && allowQuit
}

// reviveAllowed rate-limits automatic window re-creation so a page that is
// broken for a persistent reason (server wedged, port hijacked) can't put the
// shell into a destroy/create loop.
func (b *nativeBridge) reviveAllowed(now time.Time) bool {
	last := b.lastRevive.Load()
	return last == 0 || now.Sub(time.Unix(0, last)) > reviveCooldown
}

// webviewRevived reports whether the page proved itself after the given show.
// Three kinds of evidence count, and any one of them clears the window:
//
//   - a frame later than the show: the compositor is producing pixels;
//   - a beat that reported the page hidden: it is legitimately not rendering
//     (minimised, fully occluded, another space) — frames are not owed;
//   - nothing else. A page that beats while claiming to be visible yet never
//     produces a frame is the black-window signature, and a page that does not
//     beat at all is a dead renderer. Both need a new window.
func (b *nativeBridge) needsRevive(shownAt time.Time) bool {
	if lastFrame := b.lastFrame.Load(); lastFrame != 0 && time.Unix(0, lastFrame).After(shownAt) {
		return false
	}
	if hidden := b.lastHiddenBeat.Load(); hidden != 0 && time.Unix(0, hidden).After(shownAt) {
		return false
	}
	return true
}

// detachStalled detaches w when it is still the current window and a revive is
// off cooldown. Identity-keyed so a probe that slept through the user closing
// the window — or through another revive — leaves the successor alone. The
// caller closes the returned window; detaching first is what lets the
// replacement survive the old window's WindowClosing handler.
func (b *nativeBridge) detachStalled(w *application.WebviewWindow, now time.Time) *application.WebviewWindow {
	b.windowMu.Lock()
	defer b.windowMu.Unlock()
	if b.window != w || !b.reviveAllowed(now) {
		return nil
	}
	b.lastRevive.Store(now.UnixNano())
	b.window = nil
	return w
}

// probeAfterShow schedules the liveness verdict for a window that was already
// open when it was shown — the two failure modes a Show can't fix by itself:
// a renderer that died (no beats at all) and a compositor that died while JS
// kept running (beats, no frames — WebView2 across sleep/wake). The window is
// replaced rather than reloaded because a broken WebView2 surface survives
// navigation. Runs off the UI thread; the window methods it calls marshal
// themselves.
func (b *nativeBridge) probeAfterShow(shown *application.WebviewWindow) {
	if shown == nil {
		return
	}
	if !b.probeInFlight.CompareAndSwap(false, true) {
		return
	}
	shownAt := time.Now()
	go func() {
		defer b.probeInFlight.Store(false)
		time.Sleep(b.probeDelay())
		if !b.needsRevive(shownAt) {
			return
		}
		old := b.detachStalled(shown, time.Now())
		if old == nil {
			return
		}
		// Create the replacement BEFORE closing the corpse. Wails' Windows
		// backend posts a quit message when the window map empties
		// (unregisterWindow, bypassing ShouldQuit), and macOS/Linux quit on
		// last-window-close unless the user keeps the hub in the background —
		// closing first would race the shell's own survival.
		b.showWindowAt("")
		old.Close()
	}()
}

// probeDelay is frameProbeDelay unless a test shortened it.
func (b *nativeBridge) probeDelay() time.Duration {
	if d := b.testProbeDelay; d > 0 {
		return d
	}
	return frameProbeDelay
}

// Heartbeat implements server.NativeBridge: the page beats every
// heartbeatInterval (and on focus/visibilitychange) with the age of the last
// requestAnimationFrame it observed, or -1 while it is hidden and owes no
// frames. The beat proves JS alive; the frame timestamp proves the render
// pipeline alive; the hidden flag says frames aren't expected.
func (b *nativeBridge) Heartbeat(frameAgeMS int64) {
	now := time.Now()
	b.lastBeat.Store(now.UnixNano())
	if frameAgeMS < 0 {
		// Hidden page (or one that has not yet seen a frame): record the beat
		// as an explicit "not rendering, by design" so the probe doesn't read
		// the missing frame as a black window.
		b.lastHiddenBeat.Store(now.UnixNano())
		return
	}
	age := time.Duration(frameAgeMS) * time.Millisecond
	if frameAgeMS > int64(maxReportedFrameAge/time.Millisecond) {
		age = maxReportedFrameAge
	}
	frame := now.Add(-age).UnixNano()
	// Monotonic: a stale report (a focus beat whose rAF has not ticked yet,
	// clock skew, a hostile local caller) must never erase newer evidence.
	for {
		prev := b.lastFrame.Load()
		if frame <= prev || b.lastFrame.CompareAndSwap(prev, frame) {
			return
		}
	}
}

// confirm shows a modal question dialog and reports whether the user chose the
// affirmative button. The cancel button is the safe default.
func (b *nativeBridge) confirm(title, message, okLabel, cancelLabel string) bool {
	var ok bool
	dlg := b.app.Dialog.Question().SetTitle(title).SetMessage(message)
	yes := dlg.AddButton(okLabel)
	yes.OnClick(func() { ok = true })
	no := dlg.AddButton(cancelLabel)
	no.OnClick(func() { ok = false })
	dlg.SetDefaultButton(no)
	dlg.SetCancelButton(no)
	dlg.Show()
	return ok
}

// showError shows a modal error dialog with a single OK button.
func (b *nativeBridge) showError(title, message string) {
	dlg := b.app.Dialog.Error().SetTitle(title).SetMessage(message)
	dlg.AddButton(L().dialogOKText).SetAsDefault()
	dlg.Show()
}

// OpenExternal opens url with the OS default handler — the release download
// page, reached from the web badge's "Download update" action (via
// /api/native/open-external), the tray "Check for updates…" flow, and chat
// links. The server has already validated the scheme (http/https/mailto/tel).
// It shells out to the
// per-platform opener rather than pulling in a third-party helper; the Wails
// runtime's own browser API isn't reachable here because the page is
// octo-served, not served off Wails' asset server (same reason ExecJS is dead
// in showWindowAt).
// CanSelfUpdate / SelfUpdate back the web badge's "Update Now" action
// (POST /api/native/self-update): available only when this build swaps itself
// in place (see canInplaceUpdate). SelfUpdate hands off to the same flow the
// tray and toast use — the native updater window takes over from here.
func (b *nativeBridge) CanSelfUpdate() bool { return b.inplaceUpdate.Load() }

func (b *nativeBridge) SelfUpdate() error {
	if !b.inplaceUpdate.Load() {
		return errors.New("this build updates through its installer; use the download link")
	}
	go startUpdateFlow(b)
	return nil
}

func (b *nativeBridge) OpenExternal(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// confirmTakeover asks whether to stop an already-running backend and become
// the hub. Declining means the app will quit (it won't run windowed without
// its own server to attach the native bridge to).
func (b *nativeBridge) confirmTakeover(pid int) bool {
	return b.confirm(L().takeoverTitle,
		fmt.Sprintf(L().takeoverMsgFmt, pid),
		L().takeoverOK, L().takeoverCancel)
}

// requestQuit is the tray "Quit Octo" action: it fully stops the backend, so it
// confirms first when channels are running (other clients would disconnect).
func (b *nativeBridge) requestQuit() {
	if srv := b.srv.Load(); srv != nil && len(srv.RunningChannels()) > 0 {
		if !b.confirm(L().quitTitle, L().quitMsg, L().quitOK, L().quitCancel) {
			return
		}
	}
	// A real quit: let ShouldQuit (Windows/Linux) allow app termination.
	b.allowQuit.Store(true)
	b.app.Quit()
}
