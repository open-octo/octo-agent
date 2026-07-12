package main

import (
	"context"
	"fmt"
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

// rememberWindowGeometry captures the window's size and maximised state into
// settings and debounces the disk write. The window is read HERE, from the
// WindowDidResize handler where it is guaranteed alive — never from the debounce
// timer or the close path. Those paths run on their own goroutines and would
// race the window's destruction: Wails' built-in WindowClosing listener marks
// the window destroyed, after which IsMaximised()/Size() short-circuit to
// false/0×0 and would clobber the freshly-saved state. The window methods are
// read before taking the lock (they marshal to the UI thread; holding
// settingsMu across that could deadlock the main thread). Size is recorded only
// while neither maximised nor fullscreen — both report a size that isn't the
// windowed size, so persisting it would corrupt the restore size a relaunch
// un-maximises back to.
func (b *nativeBridge) rememberWindowGeometry(w *application.WebviewWindow) {
	maximised := w.IsMaximised()
	fullscreen := w.IsFullscreen()
	width, height := w.Size()

	b.settingsMu.Lock()
	defer b.settingsMu.Unlock()
	b.settings.WindowMaximised = maximised
	if !maximised && !fullscreen && width > 0 && height > 0 {
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

// Notify raises an OS-native notification. No-op when the notifications service
// isn't available (an unbundled dev binary — the service needs a bundle id).
// Best-effort by contract: a delivery failure (e.g. permission not yet granted)
// is swallowed.
func (b *nativeBridge) Notify(title, body string) {
	if b.notifier == nil {
		return
	}
	_ = b.notifier.SendNotification(notifications.NotificationOptions{
		Title: title,
		Body:  body,
	})
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

// PersistChannelsEnabled records the "run channels on this machine" preference
// so a relaunch honors it (the desktop reads it at startup to seed the server).
// The live start/stop is the server's own SetChannelsEnabled, driven from the
// same request; this only writes ~/.octo/desktop.json.
func (b *nativeBridge) PersistChannelsEnabled(enabled bool) error {
	b.settingsMu.Lock()
	b.settings.ChannelsEnabled = enabled
	snapshot := b.settings
	b.settingsMu.Unlock()
	return saveDesktopSettings(snapshot)
}

// ToggleMaximise maximises or restores the window (the double-click-titlebar
// zoom the frontend can't trigger itself). No-op before the window exists.
func (b *nativeBridge) ToggleMaximise() {
	if b.window != nil {
		b.window.ToggleMaximise()
	}
}

// showWindow brings the hub window to the foreground on the current view.
func (b *nativeBridge) showWindow() { b.showWindowAt("") }

// openSettings brings the window up on the Settings view (tray "Settings").
func (b *nativeBridge) openSettings() { b.showWindowAt("settings") }

// showWindowAt brings the hub window to the foreground, re-creating it if it was
// closed to the tray (KeepRunningInBackground), and navigates to the given
// frontend hash route (empty = leave it where it is). The frontend routes on
// location.hash, so a fresh window loads straight into the view and an existing
// one navigates via a hashchange — no full reload.
func (b *nativeBridge) showWindowAt(hash string) {
	target := b.url
	if hash != "" {
		target = b.url + "#" + hash
	}
	if b.window == nil {
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
			// Hidden-inset title bar: the traffic lights float over the page's
			// top-left; the frontend insets its header past them (nativeShell).
			Mac: application.MacWindow{TitleBar: application.MacTitleBarHiddenInset},
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
			b.window = nil
		})
		// Capture size/maximised changes as the user drags or zooms the window.
		w.OnWindowEvent(events.Common.WindowDidResize, func(*application.WindowEvent) {
			b.rememberWindowGeometry(w)
		})
		b.window = w
	} else if hash != "" {
		// Already open — navigate to the route. ExecJS can't be used here: the
		// page is served by octo's own server, not Wails' asset server, so the
		// Wails runtime never loads and ExecJS stays queued forever. SetURL is a
		// native navigation that doesn't depend on it.
		b.window.SetURL(target)
	}
	b.window.Show()
	b.window.Restore()
	b.window.Focus()
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

// showInfo shows a modal informational dialog with a single OK button (the
// "you're on the latest version" outcome of the tray update check).
func (b *nativeBridge) showInfo(title, message string) {
	dlg := b.app.Dialog.Info().SetTitle(title).SetMessage(message)
	dlg.AddButton(L().dialogOKText).SetAsDefault()
	dlg.Show()
}

// OpenExternal opens url in the user's default browser — the release download
// page, reached from the web badge's "Download update" action (via
// /api/native/open-external) and the tray "Check for updates…" flow. The server
// has already validated the scheme is http/https. It shells out to the
// per-platform opener rather than pulling in a third-party helper; the Wails
// runtime's own browser API isn't reachable here because the page is
// octo-served, not served off Wails' asset server (same reason ExecJS is dead
// in showWindowAt).
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
	if srv := b.srv.Load(); srv != nil && srv.ChannelsEnabled() {
		if !b.confirm(L().quitTitle, L().quitMsg, L().quitOK, L().quitCancel) {
			return
		}
	}
	// A real quit: let ShouldQuit (Windows/Linux) allow app termination.
	b.allowQuit.Store(true)
	b.app.Quit()
}
