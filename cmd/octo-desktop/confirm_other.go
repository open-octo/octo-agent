//go:build !darwin

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// platformConfirm asks a yes/no question through Wails' own dialog, blocking
// until it is answered. Only macOS needs its own alert (see confirm_darwin.go);
// elsewhere the framework's dialog is what it has always been.
func platformConfirm(app *application.App, title, message, okLabel, cancelLabel string) bool {
	var ok bool
	dlg := app.Dialog.Question().SetTitle(title).SetMessage(message)
	yes := dlg.AddButton(okLabel)
	yes.OnClick(func() { ok = true })
	no := dlg.AddButton(cancelLabel)
	no.OnClick(func() { ok = false })
	dlg.SetDefaultButton(no)
	dlg.SetCancelButton(no)
	dlg.Show()
	return ok
}
