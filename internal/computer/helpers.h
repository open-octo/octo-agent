// C helpers for the macOS computer-use substrate. Included (as static inline)
// by each CGO translation unit in this package — cgo preambles don't share
// declarations across files.
#ifndef OCTO_COMPUTER_HELPERS_H
#define OCTO_COMPUTER_HELPERS_H

#include <ApplicationServices/ApplicationServices.h>
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <unistd.h>

// ── global HID event posting (foreground mode) ─────────────────────────────

static inline void octoPost(CGEventRef ev) {
	CGEventPost(kCGHIDEventTap, ev);
	CFRelease(ev);
}

// octoRequestAccessibility pops the system grant dialog (no-op if granted).
static inline int octoRequestAccessibility(void) {
	CFStringRef keys[1] = { kAXTrustedCheckOptionPrompt };
	CFBooleanRef vals[1] = { kCFBooleanTrue };
	CFDictionaryRef opts = CFDictionaryCreate(kCFAllocatorDefault,
		(const void **)keys, (const void **)vals, 1,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	Boolean ok = AXIsProcessTrustedWithOptions(opts);
	CFRelease(opts);
	return ok ? 1 : 0;
}

static inline int octoMouseMove(double x, double y) {
	octoPost(CGEventCreateMouseEvent(NULL, kCGEventMouseMoved, CGPointMake(x, y), kCGMouseButtonLeft));
	return 0;
}

static inline int octoClick(int right, double x, double y, int state) {
	CGMouseButton btn = right ? kCGMouseButtonRight : kCGMouseButtonLeft;
	CGEventType down = right ? kCGEventRightMouseDown : kCGEventLeftMouseDown;
	CGEventType up = right ? kCGEventRightMouseUp : kCGEventLeftMouseUp;
	CGPoint p = CGPointMake(x, y);
	for (int i = 0; i < state; i++) {
		CGEventRef d = CGEventCreateMouseEvent(NULL, down, p, btn);
		CGEventSetIntegerValueField(d, kCGMouseEventClickState, state);
		octoPost(d);
		CGEventRef u = CGEventCreateMouseEvent(NULL, up, p, btn);
		CGEventSetIntegerValueField(u, kCGMouseEventClickState, state);
		octoPost(u);
	}
	return 0;
}

static inline int octoScroll(double dx, double dy) {
	// wheel1 positive scrolls content down (view up); flip so +dy = scroll down.
	octoPost(CGEventCreateScrollWheelEvent(NULL, kCGScrollEventUnitLine, 2, (int32_t)-dy, (int32_t)dx));
	return 0;
}

static inline int octoKey(uint16_t code, uint64_t flags, int down) {
	CGEventRef ev = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)code, down ? true : false);
	CGEventSetFlags(ev, (CGEventFlags)flags);
	octoPost(ev);
	return 0;
}

static inline int octoType(const uint16_t *chars, int n) {
	for (int i = 0; i < n; i++) {
		UniChar c = (UniChar)chars[i];
		CGEventRef d = CGEventCreateKeyboardEvent(NULL, 0, true);
		CGEventKeyboardSetUnicodeString(d, 1, &c);
		octoPost(d);
		CGEventRef u = CGEventCreateKeyboardEvent(NULL, 0, false);
		CGEventKeyboardSetUnicodeString(u, 1, &c);
		octoPost(u);
	}
	return 0;
}

// octoFindWindow locates the front-most normal (layer 0) window of the app
// named owner, returning its pid, window id and bounds in global points.
static inline int octoFindWindow(const char *owner, int *pid, int *winID,
	double *x, double *y, double *w, double *h) {
	CFArrayRef list = CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly, kCGNullWindowID);
	if (!list) return -1;
	CFStringRef want = CFStringCreateWithCString(kCFAllocatorDefault, owner, kCFStringEncodingUTF8);
	int found = 0;
	CFIndex n = CFArrayGetCount(list);
	for (CFIndex i = 0; i < n && !found; i++) {
		CFDictionaryRef d = (CFDictionaryRef)CFArrayGetValueAtIndex(list, i);
		CFStringRef o = (CFStringRef)CFDictionaryGetValue(d, kCGWindowOwnerName);
		if (!o || CFStringCompare(o, want, 0) != kCFCompareEqualTo) continue;
		CFNumberRef layerN = (CFNumberRef)CFDictionaryGetValue(d, kCGWindowLayer);
		int layer = -1;
		if (layerN) CFNumberGetValue(layerN, kCFNumberIntType, &layer);
		if (layer != 0) continue;
		CFDictionaryRef b = (CFDictionaryRef)CFDictionaryGetValue(d, kCGWindowBounds);
		CGRect r = CGRectZero;
		if (!b || !CGRectMakeWithDictionaryRepresentation(b, &r)) continue;
		if (r.size.width < 50 || r.size.height < 50) continue;
		CFNumberRef idN = (CFNumberRef)CFDictionaryGetValue(d, kCGWindowNumber);
		CFNumberRef pidN = (CFNumberRef)CFDictionaryGetValue(d, kCGWindowOwnerPID);
		CFNumberGetValue(idN, kCFNumberIntType, winID);
		CFNumberGetValue(pidN, kCFNumberIntType, pid);
		*x = r.origin.x; *y = r.origin.y; *w = r.size.width; *h = r.size.height;
		found = 1;
	}
	CFRelease(want);
	CFRelease(list);
	return found ? 0 : -2;
}

static inline int octoTypePid(int pid, const uint16_t *chars, int n) {
	for (int i = 0; i < n; i++) {
		UniChar c = (UniChar)chars[i];
		CGEventRef d = CGEventCreateKeyboardEvent(NULL, 0, true);
		CGEventKeyboardSetUnicodeString(d, 1, &c);
		CGEventPostToPid((pid_t)pid, d);
		CFRelease(d);
		CGEventRef u = CGEventCreateKeyboardEvent(NULL, 0, false);
		CGEventKeyboardSetUnicodeString(u, 1, &c);
		CGEventPostToPid((pid_t)pid, u);
		CFRelease(u);
		// A background app's runloop is throttled; a burst of events gets
		// coalesced and characters drop. Pace them.
		usleep(30000);
	}
	return 0;
}

// octoClickPid delivers a click straight into one process's event queue: no
// global cursor move, no focus change. Measured on macOS 15 to be dropped by
// both SwiftUI (Calculator) and custom Metal (Chess) views even with the
// window-number fields set — kept for reference, prefer the AX path.
static inline int octoClickPid(int pid, int winID, int right, double x, double y, int state) {
	CGMouseButton btn = right ? kCGMouseButtonRight : kCGMouseButtonLeft;
	CGEventType down = right ? kCGEventRightMouseDown : kCGEventLeftMouseDown;
	CGEventType up = right ? kCGEventRightMouseUp : kCGEventLeftMouseUp;
	CGPoint p = CGPointMake(x, y);
	CGEventRef m = CGEventCreateMouseEvent(NULL, kCGEventMouseMoved, p, btn);
	CGEventSetIntegerValueField(m, kCGMouseEventWindowUnderMousePointer, winID);
	CGEventSetIntegerValueField(m, kCGMouseEventWindowUnderMousePointerThatCanHandleThisEvent, winID);
	CGEventPostToPid((pid_t)pid, m);
	CFRelease(m);
	for (int i = 0; i < state; i++) {
		CGEventRef d = CGEventCreateMouseEvent(NULL, down, p, btn);
		CGEventSetIntegerValueField(d, kCGMouseEventClickState, state);
		CGEventSetIntegerValueField(d, kCGMouseEventWindowUnderMousePointer, winID);
		CGEventSetIntegerValueField(d, kCGMouseEventWindowUnderMousePointerThatCanHandleThisEvent, winID);
		CGEventPostToPid((pid_t)pid, d);
		CFRelease(d);
		CGEventRef u = CGEventCreateMouseEvent(NULL, up, p, btn);
		CGEventSetIntegerValueField(u, kCGMouseEventClickState, state);
		CGEventSetIntegerValueField(u, kCGMouseEventWindowUnderMousePointer, winID);
		CGEventSetIntegerValueField(u, kCGMouseEventWindowUnderMousePointerThatCanHandleThisEvent, winID);
		CGEventPostToPid((pid_t)pid, u);
		CFRelease(u);
	}
	return 0;
}

// octoAXSetFrontmost brings pid's app forward — the standard third-party way
// apps raise another app (Rectangle, Hammerspoon use the same attribute).
// This is the real fix for pixel-channel input silently missing a
// backgrounded app: octoClickPid/octoTypePid above were measured to be
// dropped by both SwiftUI and custom-drawn views even while "delivered", so
// genuine focus is the only reliable path.
static inline int octoAXSetFrontmost(int pid) {
	AXUIElementRef app = AXUIElementCreateApplication((pid_t)pid);
	AXError rc = AXUIElementSetAttributeValue(app, kAXFrontmostAttribute, kCFBooleanTrue);
	CFRelease(app);
	return (int)rc;
}

// octoFrontmostAppName reads the owner name of the front-most normal-layer
// on-screen window: CGWindowListCopyWindowInfo with
// kCGWindowListOptionOnScreenOnly returns windows in front-to-back z-order,
// so the first layer-0 entry belongs to the frontmost app. Caller frees with
// free(); NULL if undetermined.
static inline char *octoFrontmostAppName(void) {
	CFArrayRef list = CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly, kCGNullWindowID);
	if (!list) return NULL;
	char *out = NULL;
	CFIndex n = CFArrayGetCount(list);
	for (CFIndex i = 0; i < n; i++) {
		CFDictionaryRef d = (CFDictionaryRef)CFArrayGetValueAtIndex(list, i);
		CFNumberRef layerN = (CFNumberRef)CFDictionaryGetValue(d, kCGWindowLayer);
		int layer = -1;
		if (layerN) CFNumberGetValue(layerN, kCFNumberIntType, &layer);
		if (layer != 0) continue;
		CFStringRef o = (CFStringRef)CFDictionaryGetValue(d, kCGWindowOwnerName);
		if (!o) break;
		CFIndex max = CFStringGetMaximumSizeForEncoding(CFStringGetLength(o), kCFStringEncodingUTF8) + 1;
		out = (char *)malloc(max);
		if (!CFStringGetCString(o, out, max, kCFStringEncodingUTF8)) { free(out); out = NULL; }
		break;
	}
	CFRelease(list);
	return out;
}

// octoDrag performs a left-button drag from (x1,y1) to (x2,y2): mouse-down at
// the start point, `steps` interpolated CGEventLeftMouseDragged events along
// a straight line to the end point (paced ~10ms apart — a crude but adequate
// stand-in for a human drag trajectory, per apps that distinguish a drag
// from a click-teleport, e.g. Lightroom Classic's crop box and mask brush),
// then mouse-up at the end point. Distinct from octoClick: no primitive here
// combines a held button with intermediate motion.
static inline int octoDrag(double x1, double y1, double x2, double y2, int steps) {
	CGPoint start = CGPointMake(x1, y1);
	octoPost(CGEventCreateMouseEvent(NULL, kCGEventLeftMouseDown, start, kCGMouseButtonLeft));
	if (steps < 1) steps = 1;
	for (int i = 1; i <= steps; i++) {
		double t = (double)i / (double)steps;
		CGPoint p = CGPointMake(x1 + (x2 - x1) * t, y1 + (y2 - y1) * t);
		octoPost(CGEventCreateMouseEvent(NULL, kCGEventLeftMouseDragged, p, kCGMouseButtonLeft));
		usleep(10000);
	}
	CGPoint end = CGPointMake(x2, y2);
	octoPost(CGEventCreateMouseEvent(NULL, kCGEventLeftMouseUp, end, kCGMouseButtonLeft));
	return 0;
}

// ── Accessibility (AX) layer ───────────────────────────────────────────────
// AX actions (AXPress / AXSetValue) are served by the target app's own AX
// server: they need no cursor, no focus, no foreground — the one true
// background-operation channel on macOS for AX-rich apps.

static inline AXUIElementRef octoAXApp(int pid) {
	return AXUIElementCreateApplication((pid_t)pid);
}

// octoAXCopyArray copies an array-valued attribute (windows / children).
// Caller CFReleases. Returns NULL when absent or empty.
static inline CFArrayRef octoAXCopyArray(AXUIElementRef el, CFStringRef attr) {
	CFTypeRef v = NULL;
	if (AXUIElementCopyAttributeValue(el, attr, &v) != kAXErrorSuccess || !v) return NULL;
	if (CFGetTypeID(v) != CFArrayGetTypeID()) { CFRelease(v); return NULL; }
	if (CFArrayGetCount((CFArrayRef)v) == 0) { CFRelease(v); return NULL; }
	return (CFArrayRef)v;
}

// octoAXCopyElement copies a single element attribute (e.g. the menu bar).
static inline AXUIElementRef octoAXCopyElement(AXUIElementRef el, CFStringRef attr) {
	CFTypeRef v = NULL;
	if (AXUIElementCopyAttributeValue(el, attr, &v) != kAXErrorSuccess || !v) return NULL;
	if (CFGetTypeID(v) != AXUIElementGetTypeID()) { CFRelease(v); return NULL; }
	return (AXUIElementRef)v;
}

// octoAXCopyString reads a string-ish attribute as newly allocated UTF-8
// (caller frees). Numbers and booleans are rendered for digest purposes.
static inline char *octoAXCopyString(AXUIElementRef el, CFStringRef attr) {
	CFTypeRef v = NULL;
	if (AXUIElementCopyAttributeValue(el, attr, &v) != kAXErrorSuccess || !v) return NULL;
	char *out = NULL;
	CFTypeID t = CFGetTypeID(v);
	if (t == CFStringGetTypeID()) {
		CFIndex max = CFStringGetMaximumSizeForEncoding(CFStringGetLength(v), kCFStringEncodingUTF8) + 1;
		out = (char *)malloc(max);
		if (!CFStringGetCString(v, out, max, kCFStringEncodingUTF8)) { free(out); out = NULL; }
	} else if (t == CFNumberGetTypeID()) {
		double d = 0;
		CFNumberGetValue(v, kCFNumberDoubleType, &d);
		char buf[64];
		snprintf(buf, sizeof buf, "%g", d);
		out = strdup(buf);
	} else if (t == CFBooleanGetTypeID()) {
		out = strdup(CFBooleanGetValue((CFBooleanRef)v) ? "true" : "false");
	}
	CFRelease(v);
	return out;
}

static inline int octoAXFrame(AXUIElementRef el, double *x, double *y, double *w, double *h) {
	CFTypeRef pv = NULL, sv = NULL;
	if (AXUIElementCopyAttributeValue(el, kAXPositionAttribute, &pv) != kAXErrorSuccess || !pv) return -1;
	if (AXUIElementCopyAttributeValue(el, kAXSizeAttribute, &sv) != kAXErrorSuccess || !sv) {
		CFRelease(pv);
		return -1;
	}
	CGPoint p = CGPointZero;
	CGSize s = CGSizeZero;
	AXValueGetValue((AXValueRef)pv, kAXValueTypeCGPoint, &p);
	AXValueGetValue((AXValueRef)sv, kAXValueTypeCGSize, &s);
	CFRelease(pv);
	CFRelease(sv);
	*x = p.x; *y = p.y; *w = s.width; *h = s.height;
	return 0;
}

static inline int octoAXPress(AXUIElementRef el) {
	return (int)AXUIElementPerformAction(el, kAXPressAction);
}

static inline int octoAXSetString(AXUIElementRef el, const char *val) {
	CFStringRef s = CFStringCreateWithCString(kCFAllocatorDefault, val, kCFStringEncodingUTF8);
	AXError rc = AXUIElementSetAttributeValue(el, kAXValueAttribute, s);
	CFRelease(s);
	return (int)rc;
}

static inline int octoAXSetDouble(AXUIElementRef el, double d) {
	CFNumberRef n = CFNumberCreate(kCFAllocatorDefault, kCFNumberDoubleType, &d);
	AXError rc = AXUIElementSetAttributeValue(el, kAXValueAttribute, n);
	CFRelease(n);
	return (int)rc;
}

#endif // OCTO_COMPUTER_HELPERS_H
