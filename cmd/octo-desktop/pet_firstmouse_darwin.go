//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>
#import <stdatomic.h>

// petWindow is the pet panel while it is up. Held as a bare pointer that is
// only ever compared, never messaged: the panel is released the instant it
// closes, so dereferencing it here would be a use-after-free. Atomic because
// petForgetWindow is called from whichever goroutine dismissed the pet, while
// the hook below reads it on the main thread.
static void * _Atomic petWindow = NULL;

// petOriginalIMPs maps each patched Class to the acceptsFirstMouse: it had
// before the patch, so every view outside the pet keeps its own answer. Touched
// only from the main thread: AppKit asks acceptsFirstMouse: there, and the
// patching runs under InvokeSync.
static CFMutableDictionaryRef petOriginalIMPs = NULL;

// petAcceptsFirstMouse answers YES inside the pet and defers to the class's own
// implementation everywhere else.
static BOOL petAcceptsFirstMouse(id self, SEL _cmd, NSEvent *event) {
	if (atomic_load(&petWindow) == (void *)[(NSView *)self window]) {
		return YES;
	}
	// The receiver's class may be a KVO-generated subclass of a patched class
	// rather than the patched class itself, so walk up to the one we know.
	for (Class c = object_getClass(self); c != Nil; c = class_getSuperclass(c)) {
		IMP orig = (IMP)CFDictionaryGetValue(petOriginalIMPs, (const void *)c);
		if (orig != NULL) {
			return ((BOOL (*)(id, SEL, NSEvent *))orig)(self, _cmd, event);
		}
	}
	return NO;
}

// petPatchClass replaces acceptsFirstMouse: on one class, remembering what was
// there before.
//
// The method table, NOT the isa pointer. Moving an instance onto a
// runtime-generated subclass — the isa-swizzle KVO itself uses — looks like it
// would be the narrower change, since it touches one view instead of a whole
// class. It is in fact the one thing that must not be done here: AppKit and
// WebKit already observe these views, so their isa points at an
// NSKVONotifying_ subclass, and slipping another class underneath leaves KVO's
// bookkeeping keyed on a class it never made. Tearing the window down then
// faults inside _NSKeyValueRetainedObservationInfoForObject, which took the
// whole app with it every time the pet was dismissed. Replacing the method
// leaves every isa alone; a KVO subclass made later simply inherits the patch.
static void petPatchClass(Class c) {
	if (c == Nil) {
		return;
	}
	if (petOriginalIMPs == NULL) {
		petOriginalIMPs = CFDictionaryCreateMutable(NULL, 0, NULL, NULL);
	}
	if (CFDictionaryContainsKey(petOriginalIMPs, (const void *)c)) {
		return;
	}
	SEL sel = @selector(acceptsFirstMouse:);
	// Resolved before the replace, so a class that inherits the method records
	// the inherited implementation rather than finding its own patch.
	IMP orig = class_getMethodImplementation(c, sel);
	CFDictionarySetValue(petOriginalIMPs, (const void *)c, (void *)orig);
	class_replaceMethod(c, sel, (IMP)petAcceptsFirstMouse, "B@:@");
}

// petInstallFirstMouse patches every class under the window. The click is
// delivered to whichever view hit-tests, which is somewhere inside the webview
// rather than the content view, so the whole tree has to be covered.
static void petInstallFirstMouse(void *nsWindow) {
	if (nsWindow == NULL) {
		return;
	}
	atomic_store(&petWindow, nsWindow);
	NSWindow *w = (NSWindow *)nsWindow;
	NSView *root = [w contentView];
	if (root == nil) {
		return;
	}
	NSMutableArray *stack = [NSMutableArray arrayWithObject:root];
	while ([stack count] > 0) {
		NSView *v = [stack lastObject];
		[stack removeLastObject];
		// [v class], not object_getClass(v): an observed view's isa names a
		// KVO subclass, and patching that would put the hook somewhere KVO
		// expects to own.
		petPatchClass([v class]);
		for (NSView *c in [v subviews]) {
			[stack addObject:c];
		}
	}
}

static void petForgetFirstMouseWindow(void) {
	atomic_store(&petWindow, NULL);
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
// Must run on the main thread.
func petAcceptFirstMouse(nsWindow unsafe.Pointer) {
	C.petInstallFirstMouse(nsWindow)
}

// petForgetFirstMouseWindow drops the pet window the hook above answers for,
// before the window is closed. Without it the patch would keep saying YES for
// whatever NSWindow the allocator later hands that same address. Safe from any
// goroutine.
func petForgetFirstMouseWindow() {
	C.petForgetFirstMouseWindow()
}
