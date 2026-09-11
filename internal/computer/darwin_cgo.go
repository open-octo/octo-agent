//go:build darwin && cgo

package computer

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreGraphics -framework CoreFoundation
#include <ApplicationServices/ApplicationServices.h>
#include <stdlib.h>

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
