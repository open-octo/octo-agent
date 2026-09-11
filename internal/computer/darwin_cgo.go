//go:build darwin && cgo

package computer

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreGraphics -framework CoreFoundation
#include <ApplicationServices/ApplicationServices.h>
#include <stdlib.h>
#include <unistd.h>

static void octoPost(CGEventRef ev) {
	CGEventPost(kCGHIDEventTap, ev);
	CFRelease(ev);
}

// octoRequestAccessibility pops the system grant dialog (no-op if granted).
static int octoRequestAccessibility(void) {
	CFStringRef keys[1] = { kAXTrustedCheckOptionPrompt };
	CFBooleanRef vals[1] = { kCFBooleanTrue };
	CFDictionaryRef opts = CFDictionaryCreate(kCFAllocatorDefault,
		(const void **)keys, (const void **)vals, 1,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	Boolean ok = AXIsProcessTrustedWithOptions(opts);
	CFRelease(opts);
	return ok ? 1 : 0;
}

static int octoMouseMove(double x, double y) {
	octoPost(CGEventCreateMouseEvent(NULL, kCGEventMouseMoved, CGPointMake(x, y), kCGMouseButtonLeft));
	return 0;
}

static int octoClick(int right, double x, double y, int state) {
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

static int octoScroll(double dx, double dy) {
	// wheel1 positive scrolls content down (view up); flip so +dy = scroll down.
	octoPost(CGEventCreateScrollWheelEvent(NULL, kCGScrollEventUnitLine, 2, (int32_t)-dy, (int32_t)dx));
	return 0;
}

static int octoKey(uint16_t code, uint64_t flags, int down) {
	CGEventRef ev = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)code, down ? true : false);
	CGEventSetFlags(ev, (CGEventFlags)flags);
	octoPost(ev);
	return 0;
}

static int octoType(const uint16_t *chars, int n) {
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
static int octoFindWindow(const char *owner, int *pid, int *winID,
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

static int octoTypePid(int pid, const uint16_t *chars, int n) {
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
// global cursor move, no focus change — the background-operation path.
static int octoClickPid(int pid, int winID, int right, double x, double y, int state) {
	CGMouseButton btn = right ? kCGMouseButtonRight : kCGMouseButtonLeft;
	CGEventType down = right ? kCGEventRightMouseDown : kCGEventLeftMouseDown;
	CGEventType up = right ? kCGEventRightMouseUp : kCGEventLeftMouseUp;
	CGPoint p = CGPointMake(x, y);
	// Some views hit-test against the cursor's current position instead of
	// the event's own location — deliver a synthetic move first so both read
	// the same point. Fields 91/92 tag the owning window: without them
	// AppKit drops mouse events that bypass the WindowServer's hit-testing.
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
*/
import "C"

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"unsafe"
)

func trusted() bool {
	return C.AXIsProcessTrusted() != 0
}

func screenCaptureAllowed() bool {
	return bool(C.CGPreflightScreenCaptureAccess())
}

func requestScreenCapture() { C.CGRequestScreenCaptureAccess() }

func requestAccessibility() { C.octoRequestAccessibility() }

func screenSize() (float64, float64, error) {
	b := C.CGDisplayBounds(C.CGMainDisplayID())
	return float64(b.size.width), float64(b.size.height), nil
}

// screenshot captures the main display via the screencapture CLI — the
// CGWindowList capture API was obsoleted in the macOS 15 SDK (ScreenCaptureKit
// is the sanctioned path but ObjC/async; the CLI keeps the spike pure-CGo-free
// for capture). The returned PNG is full-resolution (Retina 2x); the caller
// maps coordinates into logical points via ScreenSize.
func screenshot() ([]byte, error) {
	path := filepath.Join(os.TempDir(), fmt.Sprintf("octo-computer-shot-%d.png", time.Now().UnixNano()))
	defer os.Remove(path)
	cmd := exec.Command("screencapture", "-x", "-t", "png", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("computer: screencapture failed: %v (%s)", err, out)
	}
	png, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("computer: reading capture: %w", err)
	}
	return png, nil
}

func moveTo(x, y float64) error {
	C.octoMouseMove(C.double(x), C.double(y))
	return nil
}

func click(button string, x, y float64, clicks int) error {
	right := C.int(0)
	if button == "right" {
		right = 1
	}
	C.octoClick(right, C.double(x), C.double(y), C.int(clicks))
	return nil
}

func scroll(dx, dy float64) error {
	C.octoScroll(C.double(dx), C.double(dy))
	return nil
}

func press(keycode uint16, flags uint64) error {
	var cgFlags uint64
	if flags&flagShift != 0 {
		cgFlags |= uint64(C.kCGEventFlagMaskShift)
	}
	if flags&flagControl != 0 {
		cgFlags |= uint64(C.kCGEventFlagMaskControl)
	}
	if flags&flagOption != 0 {
		cgFlags |= uint64(C.kCGEventFlagMaskAlternate)
	}
	if flags&flagCommand != 0 {
		cgFlags |= uint64(C.kCGEventFlagMaskCommand)
	}
	C.octoKey(C.uint16_t(keycode), C.uint64_t(cgFlags), 1)
	time.Sleep(30 * time.Millisecond)
	C.octoKey(C.uint16_t(keycode), C.uint64_t(cgFlags), 0)
	return nil
}

func typeText(s string) error {
	// Encode as UTF-16 (UniChar) for CGEventKeyboardSetUnicodeString.
	u16 := make([]uint16, 0, len(s))
	for _, r := range s {
		if r > 0xFFFF {
			r -= 0x10000
			u16 = append(u16, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			u16 = append(u16, uint16(r))
		}
	}
	C.octoType((*C.uint16_t)(unsafe.Pointer(&u16[0])), C.int(len(u16)))
	return nil
}

func findWindow(owner string) (Window, error) {
	cowner := C.CString(owner)
	defer C.free(unsafe.Pointer(cowner))
	var pid, id C.int
	var x, y, w, h C.double
	rc := C.octoFindWindow(cowner, &pid, &id, &x, &y, &w, &h)
	if rc != 0 {
		return Window{}, fmt.Errorf("computer: no on-screen window found for %q (rc=%d) — is the app open and not minimized?", owner, int(rc))
	}
	return Window{PID: int(pid), ID: int(id), X: float64(x), Y: float64(y), W: float64(w), H: float64(h)}, nil
}

func clickPid(pid, winID int, button string, x, y float64, clicks int) error {
	right := C.int(0)
	if button == "right" {
		right = 1
	}
	C.octoClickPid(C.int(pid), C.int(winID), right, C.double(x), C.double(y), C.int(clicks))
	return nil
}

func typeTextPid(pid int, s string) error {
	u16 := make([]uint16, 0, len(s))
	for _, r := range s {
		if r > 0xFFFF {
			r -= 0x10000
			u16 = append(u16, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			u16 = append(u16, uint16(r))
		}
	}
	C.octoTypePid(C.int(pid), (*C.uint16_t)(unsafe.Pointer(&u16[0])), C.int(len(u16)))
	return nil
}
