//go:build windows

package computer

import (
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// INPUT is 40 bytes on 64-bit Windows: DWORD type, pointer-aligned union of
// MOUSEINPUT (32 bytes). A drift here means SendInput rejects every event.
func TestInputLayout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		if got := unsafe.Sizeof(input{}); got != 40 {
			t.Fatalf("sizeof(INPUT) = %d, want 40", got)
		}
		if off := unsafe.Offsetof(input{}.mi); off != 8 {
			t.Fatalf("offsetof(INPUT.union) = %d, want 8", off)
		}
	}
	if got := unsafe.Sizeof(bitmapInfoHeader{}); got != 40 {
		t.Fatalf("sizeof(BITMAPINFOHEADER) = %d, want 40", got)
	}
	var in input
	in.keyboard().wVk = vkReturn
	if in.mi.dx&0xFFFF != vkReturn {
		t.Fatal("keyboard view must overlay the union's first bytes")
	}
}

func TestWinKeyCodeNamed(t *testing.T) {
	cases := map[string]struct {
		vk  uint16
		ext bool
	}{
		"enter": {vkReturn, false}, "escape": {vkEscape, false}, "tab": {vkTab, false},
		"backspace": {vkBack, false}, "delete": {vkDelete, true},
		"left": {vkLeft, true}, "pagedown": {vkNext, true}, "f12": {0x7B, false},
	}
	for key, want := range cases {
		vk, ext, shift, err := winKeyCode(key)
		if err != nil || vk != want.vk || ext != want.ext || shift {
			t.Errorf("winKeyCode(%q) = (0x%02X, %v, %v, %v), want (0x%02X, %v, false, nil)", key, vk, ext, shift, err, want.vk, want.ext)
		}
	}
	if _, _, _, err := winKeyCode("nosuchkey"); err == nil {
		t.Error("unknown key name must error")
	}
}

// Single characters resolve through the live keyboard layout; on the
// runner's en-US layout "a" is VK 0x41 unshifted and "A" needs Shift.
func TestWinKeyCodeChar(t *testing.T) {
	vk, _, shift, err := winKeyCode("a")
	if err != nil {
		t.Fatal(err)
	}
	if vk != 0x41 || shift {
		t.Errorf(`winKeyCode("a") = (0x%02X, shift=%v), want (0x41, false)`, vk, shift)
	}
	if _, _, shift, err := winKeyCode("A"); err != nil || !shift {
		t.Errorf(`winKeyCode("A") shift=%v err=%v, want shift=true`, shift, err)
	}
}

func TestControlTypeName(t *testing.T) {
	if got := controlTypeName(50000); got != "Button" {
		t.Errorf("50000 = %q, want Button", got)
	}
	if got := controlTypeName(50032); got != "Window" {
		t.Errorf("50032 = %q, want Window", got)
	}
	if got := controlTypeName(50040); got != "AppBar" {
		t.Errorf("50040 = %q, want AppBar", got)
	}
	if got := controlTypeName(1); !strings.HasPrefix(got, "ControlType") {
		t.Errorf("unknown id = %q", got)
	}
}

func TestScreenSize(t *testing.T) {
	w, h, err := screenSize()
	if err != nil || w <= 0 || h <= 0 {
		t.Fatalf("screenSize() = (%v, %v, %v)", w, h, err)
	}
}

func TestFindWindowMissing(t *testing.T) {
	if _, err := findWindow("octo-no-such-app-8f3a"); err == nil {
		t.Fatal("a non-existent app must not resolve to a window")
	}
	if _, err := findWindow(""); err == nil {
		t.Fatal("empty owner must error")
	}
}

// COM plumbing smoke test: CoCreateInstance(CUIAutomation), ElementFromHandle
// on the desktop window, and a control-type read through the vtable. This is
// the deepest check available without an interactive session — it exercises
// the GUIDs and the IUIAutomation / IUIAutomationElement slot numbers.
func TestUIADesktopElement(t *testing.T) {
	err := withCOM(func(auto comObj) error {
		root, err := elementFromHandle(auto, uintptr(windows.GetDesktopWindow()))
		if err != nil {
			return err
		}
		defer root.release()
		ct := elementControlType(root)
		if ct < uiaFirstControlTypeId || int(ct-uiaFirstControlTypeId) >= len(uiaControlTypeNames) {
			t.Errorf("desktop element control type = %d, outside the known range", ct)
		}
		walker, err := controlViewWalker(auto)
		if err != nil {
			return err
		}
		defer walker.release()
		// Walking the desktop's direct children must not crash; the count is
		// session-dependent so only the mechanics are asserted.
		n := 0
		walkChildren(walker, root, func(child comObj) bool {
			_ = uiaElement(child, 1)
			n++
			return n < 5
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
