//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#import <Cocoa/Cocoa.h>

// petCursorPos writes the cursor position in screen points with the origin at
// the TOP-left of the primary display. AppKit reports mouseLocation from the
// BOTTOM-left, which is the opposite of the convention WebviewWindow.Bounds()
// uses, so the flip happens here rather than being re-derived at every call
// site.
static void petCursorPos(double *x, double *y) {
	NSPoint p = [NSEvent mouseLocation];
	NSArray<NSScreen *> *screens = [NSScreen screens];
	double h = 0;
	if ([screens count] > 0) {
		h = [[screens objectAtIndex:0] frame].size.height;
	}
	*x = p.x;
	*y = h - p.y;
}
*/
import "C"

// petCursor returns the cursor position in the same coordinate space as
// WebviewWindow.Bounds(). Reading it does not require the window to be
// receiving mouse events, which is the whole point: a window that is ignoring
// the mouse never sees the cursor come back.
func petCursor() (x int, y int, ok bool) {
	var cx, cy C.double
	C.petCursorPos(&cx, &cy)
	return int(cx), int(cy), true
}
