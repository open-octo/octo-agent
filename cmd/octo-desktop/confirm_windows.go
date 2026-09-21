//go:build windows

package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

// idYes is MessageBox's "Yes" response (IDYES in winuser.h). x/sys/windows
// carries the MB_* request flags but none of the ID* responses.
const idYes = 6

// confirmFlags is the MessageBox style every platformConfirm question uses.
//
// MB_SYSTEMMODAL is what Wails' own question dialog passes, and keeps the box
// above other windows. MB_SETFOREGROUND is the addition: these prompts are
// raised from the tray, with the app's window closed and the user in another
// program, and without it the box can open behind whatever they are looking at
// — the tray click then appears to do nothing, which is the same failure the
// macOS path fixes with a floating window level.
const confirmFlags = windows.MB_YESNO | windows.MB_ICONQUESTION |
	windows.MB_SYSTEMMODAL | windows.MB_SETFOREGROUND

// confirmAnswer reports whether a MessageBox return code is a yes. Anything
// else — "No", or the 0 an error returns — is a no, because every caller acts
// only on a yes (quit, stop another backend, restart into another profile).
func confirmAnswer(ret int32) bool { return ret == idYes }

// platformConfirm asks a yes/no question, blocking until it is answered.
//
// Windows drives MessageBox itself rather than going through Wails' dialog,
// which cannot report which button was pressed. A question dialog there is a
// plain MessageBox with MB_YESNO — the system's own localised buttons — and
// Wails dispatches the button callbacks by mapping the return code to a fixed
// English string ("Yes"/"No"/"Ok"/"Cancel") and comparing it to the label
// passed to AddButton. Ours are "Quit"/"Cancel" ("退出"/"取消"), which match
// nothing, so no callback ever ran and the answer was always the zero value:
// "Quit Octo" from the tray put the prompt up and then did nothing whichever
// button was clicked, and the takeover and profile-switch prompts silently
// cancelled themselves the same way (#2503).
//
// okLabel/cancelLabel are unavoidably ignored — MessageBox draws the system's
// Yes/No and takes no custom labels. Nothing changes on screen (those are the
// buttons Wails put there too); every message that reaches here ends in a
// yes/no question, so they read correctly.
//
// InvokeSync for the same reasons as the macOS path: the modal loop runs
// inside the call, so the answer is the return value, and it matches where
// Wails ran its dialog. Callers are on a menu callback's or an application
// event listener's goroutine, and a main-thread caller runs it inline, so the
// wait cannot deadlock.
func platformConfirm(_ *application.App, title, message, _, _ string) bool {
	// UTF16PtrFromString only fails on an embedded NUL, which none of these
	// messages can contain. Refusing rather than panicking on it keeps a
	// hypothetical bad string from taking the app down mid-quit.
	titlePtr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return false
	}
	messagePtr, err := windows.UTF16PtrFromString(message)
	if err != nil {
		return false
	}
	var ret int32
	application.InvokeSync(func() {
		// A zero owner window, as Wails passes when no window is attached to
		// the dialog: the window these prompts belong to is usually hidden to
		// the tray, and a hidden owner would place the box against it.
		ret, _ = windows.MessageBox(0, messagePtr, titlePtr, confirmFlags)
	})
	return confirmAnswer(ret)
}
