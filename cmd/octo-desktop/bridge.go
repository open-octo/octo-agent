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
	notifier *notifications.NotificationService
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

// Notify raises an OS-native notification. Best-effort: a delivery failure
// (e.g. notifications not authorized yet) is swallowed, matching the
// interface's contract that the host owns its own logging.
func (b *nativeBridge) Notify(title, body string) {
	_ = b.notifier.SendNotification(notifications.NotificationOptions{
		Title: title,
		Body:  body,
	})
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
