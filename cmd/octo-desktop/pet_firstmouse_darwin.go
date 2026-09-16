//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>
#import <string.h>

static const char *petSubclassSuffix = "_OctoPetFirstMouse";

static BOOL petAcceptsFirstMouse(id self, SEL _cmd, NSEvent *event) { return YES; }

// petPatchView moves ONE view instance onto a dynamic subclass whose
// acceptsFirstMouse: returns YES — the same isa-swizzle KVO uses. Replacing the
// method on the class itself would change every WKWebView in the process, the
// main window's included; this touches only the pet's own views.
static void petPatchView(NSView *v) {
	Class orig = object_getClass(v);
	const char *origName = class_getName(orig);
	if (strstr(origName, petSubclassSuffix) != NULL) {
		return; // already moved onto our subclass
	}
	NSString *n = [NSString stringWithFormat:@"%s%s", origName, petSubclassSuffix];
	const char *name = [n UTF8String];
	Class sub = objc_lookUpClass(name);
	if (sub == Nil) {
		sub = objc_allocateClassPair(orig, name, 0);
		if (sub == Nil) {
			return;
		}
		class_addMethod(sub, @selector(acceptsFirstMouse:), (IMP)petAcceptsFirstMouse, "B@:@");
		objc_registerClassPair(sub);
	}
	object_setClass(v, sub);
}

// petInstallFirstMouse patches every view under the window. The click is
// delivered to whichever view hit-tests, which is somewhere inside the webview
// rather than the content view, so the whole tree has to be covered.
static void petInstallFirstMouse(void *nsWindow) {
	if (nsWindow == NULL) {
		return;
	}
	NSWindow *w = (__bridge NSWindow *)nsWindow;
	NSView *root = [w contentView];
	if (root == nil) {
		return;
	}
	NSMutableArray *stack = [NSMutableArray array];
	[stack addObject:root];
	while ([stack count] > 0) {
		NSView *v = [stack lastObject];
		[stack removeLastObject];
		petPatchView(v);
		for (NSView *c in [v subviews]) {
			[stack addObject:c];
		}
	}
}
*/
import "C"

import "unsafe"

// petAcceptFirstMouse makes the pet's views act on a click that arrives while
// the window is not key, instead of spending it on becoming key.
//
// macOS gives a non-key window's first click to the window, not to the view
// under it, unless that view answers acceptsFirstMouse: with YES. For a window
// whose entire interaction is one poke that is the wrong default — and it also
// explains why the cursor only turned into the page's grab hand after a click:
// AppKit applies a window's cursor rects once it is key.
//
// Wails exposes no acceptsFirstMouse option (and MacPanelPreferences'
// BecomesKeyOnlyIfNeeded governs whether the panel takes key status, not
// whether the click is delivered), so this reaches through NativeWindow().
func petAcceptFirstMouse(nsWindow unsafe.Pointer) {
	C.petInstallFirstMouse(nsWindow)
}
