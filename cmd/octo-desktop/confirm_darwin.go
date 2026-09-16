//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#import <Cocoa/Cocoa.h>
#include <stdlib.h>

// nativeConfirm shows the same app-modal NSAlert Wails' Dialog.Question builds,
// with two properties set on its window before runModal takes over the main
// thread. Returns true when the user picked okLabel.
//
// Setting them is the whole reason this does not go through Wails: its dialog
// is created and run inside one main-thread block, so the window exists only
// once the modal loop already owns the thread. Reaching it from outside that
// block was tried and does not work — a loop that polled [NSApp modalWindow]
// every 50ms from another goroutine came back empty for the whole time the
// alert was up, and only ran to completion after it closed. Whatever the
// mechanism, there is no window to configure until it is too late to matter.
//
// Raising the app instead would be the obvious fix and cannot be relied on.
// macOS lets an app activate itself only while the user has recently been in
// it; in the state this bug is reported from — window closed to the tray, the
// app left alone for a while — activateIgnoringOtherApps: from a tray click is
// silently ignored and [NSApp isActive] stays false. (So does
// requestUserAttention:.) Both properties below ask for none of that: they
// describe the window, not who owns the focus, and hold in either state.
//
//   - Floating level puts the alert above other apps' normal windows. Without
//     it the alert opens behind whatever the user is looking at, which is the
//     bug: the tray click appears to do nothing.
//   - The non-activating panel mask lets it become the key window while the app
//     stays inactive. Without it the alert is drawn in the inactive appearance
//     (grey, no blue default button) and takes no keyboard until clicked.
//
// Ordering the window front by hand is deliberately NOT done: it shows the
// window before NSAlert has laid it out, so the alert comes up mis-sized and
// away from its centred position. runModal presents it correctly on its own.
static bool nativeConfirm(char *title, char *message, char *okLabel, char *cancelLabel) {
	@autoreleasepool {
		NSAlert *alert = [[NSAlert alloc] init];
		[alert setAlertStyle:NSAlertStyleInformational];
		[alert setMessageText:[NSString stringWithUTF8String:title]];
		[alert setInformativeText:[NSString stringWithUTF8String:message]];
		// Cancel is added first so it lands rightmost and takes Return, which is
		// the arrangement the Wails path produces (it adds the buttons in
		// reverse and marks cancel as the default).
		[alert addButtonWithTitle:[NSString stringWithUTF8String:cancelLabel]];
		[alert addButtonWithTitle:[NSString stringWithUTF8String:okLabel]];

		NSWindow *window = [alert window];
		[window setLevel:NSFloatingWindowLevel];
		// The mask is a panel-only style; that NSAlert backs itself with an
		// NSPanel is AppKit's business, so ask before assuming it. A future
		// AppKit that hands back a plain NSWindow loses the key-window
		// appearance rather than raising for an invalid style mask.
		if ([window isKindOfClass:[NSPanel class]]) {
			[window setStyleMask:[window styleMask] | NSWindowStyleMaskNonactivatingPanel];
		}

		NSModalResponse response = [alert runModal];
		[alert release];
		return response == NSAlertSecondButtonReturn;
	}
}
*/
import "C"

import (
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// platformConfirm asks a yes/no question, blocking until it is answered.
//
// InvokeSync because AppKit wants the alert on the main thread, and because the
// answer is the return value: the modal loop runs inside the call. Every caller
// is on a menu callback's or an application-event listener's goroutine — Wails
// dispatches both with `go` — so the wait cannot deadlock against the main
// thread it is waiting for.
func platformConfirm(_ *application.App, title, message, okLabel, cancelLabel string) bool {
	cTitle := C.CString(title)
	cMessage := C.CString(message)
	cOK := C.CString(okLabel)
	cCancel := C.CString(cancelLabel)
	defer func() {
		C.free(unsafe.Pointer(cTitle))
		C.free(unsafe.Pointer(cMessage))
		C.free(unsafe.Pointer(cOK))
		C.free(unsafe.Pointer(cCancel))
	}()
	var ok bool
	application.InvokeSync(func() {
		ok = bool(C.nativeConfirm(cTitle, cMessage, cOK, cCancel))
	})
	return ok
}
