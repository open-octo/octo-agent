//go:build !darwin && !windows

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// platformConfirm asks a yes/no question through Wails' own dialog, blocking
// until it is answered. Linux keeps the framework's dialog — its buttons carry
// our labels and report which one was pressed, neither of which is true on
// Windows (see confirm_windows.go), and it needs none of what macOS's own alert
// is for (confirm_darwin.go).
//
// What it does NOT do is block: Show hands the dialog to the GTK backend, which
// runs it on a goroutine of its own and returns immediately, so reading a
// variable the button callbacks set gave an answer before the dialog was even
// on screen — always the zero value, "cancel". Waiting for a callback is what
// makes this synchronous, and the wait ends on both of the paths that report
// an answer: a button press, and the dialog's close-request, which reports the
// cancel button set below (in the GTK4 backend octo builds — the gtk3 build
// tag is used nowhere here — Escape activates that button too). Those two are
// the whole contract: a dialog torn down by some other route would report
// nothing and leave this call waiting, so nothing here may take one down by
// hand.
//
// Every caller is on a menu callback's or an application event listener's
// goroutine — Wails dispatches both with `go` — so the wait is never holding
// the UI thread the dialog needs.
func platformConfirm(app *application.App, title, message, okLabel, cancelLabel string) bool {
	// Buffered: a callback must never block on the send, and only the first
	// answer is read.
	answer := make(chan bool, 2)
	dlg := app.Dialog.Question().SetTitle(title).SetMessage(message)
	yes := dlg.AddButton(okLabel)
	yes.OnClick(func() { answer <- true })
	no := dlg.AddButton(cancelLabel)
	no.OnClick(func() { answer <- false })
	dlg.SetDefaultButton(no)
	dlg.SetCancelButton(no)
	dlg.Show()
	return <-answer
}
