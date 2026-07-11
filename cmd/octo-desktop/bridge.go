package main

import (
	"context"

	"github.com/wailsapp/wails/v3/pkg/application"
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

// showWindow brings the window back to the foreground — used by the tray's
// "Show Octo" item and when a second instance is launched.
func (b *nativeBridge) showWindow() {
	if b.window == nil {
		return
	}
	b.window.Show()
	b.window.Restore()
	b.window.Focus()
}
