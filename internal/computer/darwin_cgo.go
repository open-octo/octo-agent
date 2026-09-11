//go:build darwin && cgo

package computer

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreGraphics -framework CoreFoundation
#include "helpers.h"
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
// is the sanctioned path but ObjC/async; the CLI keeps capture CGo-free). The
// returned PNG is full-resolution (Retina 2x); the caller maps coordinates
// into logical points via ScreenSize.
//
// -D 1 pins the capture to the main display (screencapture(1): "1 is main"),
// the only display ScreenSize's coordinate space describes.
func screenshot() ([]byte, error) {
	path := filepath.Join(os.TempDir(), fmt.Sprintf("octo-computer-shot-%d.png", time.Now().UnixNano()))
	defer os.Remove(path)
	cmd := exec.Command("screencapture", "-x", "-D", "1", "-t", "png", path)
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

// macKeyCodes maps canonical key names (see keyAliases) to macOS virtual
// keycodes.
var macKeyCodes = map[string]uint16{
	"enter": 36, "tab": 48, "space": 49,
	"backspace": 51, "delete": 117, "escape": 53,
	"left": 123, "right": 124, "down": 125, "up": 126,
	"home": 115, "end": 119, "pageup": 116, "pagedown": 121,
	"f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96, "f6": 97,
	"f7": 98, "f8": 100, "f9": 101, "f10": 109, "f11": 103, "f12": 111,
}

// macCharKeyCodes maps a single printable ASCII character to the US-layout
// macOS keycode that produces it (unshifted).
var macCharKeyCodes = map[byte]uint16{
	'a': 0, 's': 1, 'd': 2, 'f': 3, 'h': 4, 'g': 5, 'z': 6, 'x': 7,
	'c': 8, 'v': 9, 'b': 11, 'q': 12, 'w': 13, 'e': 14, 'r': 15,
	'y': 16, 't': 17, '1': 18, '2': 19, '3': 20, '4': 21, '6': 22,
	'5': 23, '=': 24, '9': 25, '7': 26, '-': 27, '8': 28, '0': 29,
	']': 30, 'o': 31, 'u': 32, '[': 33, 'i': 34, 'p': 35, 'l': 37,
	'j': 38, '\'': 39, 'k': 40, ';': 41, '\\': 42, ',': 43, '/': 44,
	'n': 45, 'm': 46, '.': 47, '`': 50,
}

// macKeyCode resolves a canonical key name to a macOS virtual keycode.
func macKeyCode(key string) (uint16, bool) {
	if kc, ok := macKeyCodes[key]; ok {
		return kc, true
	}
	if len(key) == 1 {
		kc, ok := macCharKeyCodes[key[0]]
		return kc, ok
	}
	return 0, false
}

func press(key string, flags uint64) error {
	keycode, ok := macKeyCode(key)
	if !ok {
		return fmt.Errorf("computer: key %q has no macOS keycode", key)
	}
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
