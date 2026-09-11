//go:build windows

package computer

import (
	"bytes"
	"image/png"
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

// The GDI capture path at runtime: BitBlt into a compatible bitmap, GetDIBits
// with the bitmap deselected, PNG encode. The runner's session may render
// black, so only shape is asserted — dimensions must equal screenSize.
func TestScreenshotPNG(t *testing.T) {
	data, err := screenshot()
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("capture is not a PNG: %v", err)
	}
	w, h, _ := screenSize()
	if b := img.Bounds(); float64(b.Dx()) != w || float64(b.Dy()) != h {
		t.Fatalf("capture is %dx%d, screen is %.0fx%.0f", b.Dx(), b.Dy(), w, h)
	}
}

// GetCurrentPattern (element slot 16) plus QueryInterface to a pattern IID,
// read-only: the desktop is an HWND-backed element, so the MSAA bridge
// pattern is expected; if a runner lacks it the slots are still exercised.
func TestUIADesktopPatternQuery(t *testing.T) {
	err := withCOM(func(auto comObj) error {
		root, err := elementFromHandle(auto, uintptr(windows.GetDesktopWindow()))
		if err != nil {
			return err
		}
		defer root.release()
		p := elementPattern(root, uiaLegacyIAccessiblePatternId, &iidIUIAutomationLegacyIAccessiblePattern)
		if p == 0 {
			t.Log("desktop element exposes no LegacyIAccessible pattern on this runner")
			return nil
		}
		defer p.release()
		// IUIAutomationLegacyIAccessiblePattern::get_CurrentName is slot 7.
		name := elementString(p, 7)
		t.Logf("desktop MSAA name: %q", name)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// findWindow must resolve the executable-name pass before falling back to a
// title substring; a nonsense executable that is also not in any title fails
// both passes, while the pid lookup path is independent of both.
func TestFindWindowPasses(t *testing.T) {
	if _, ok := enumerate("octo-no-such-exe-8f3a", false, 0); ok {
		t.Fatal("executable pass matched a nonsense name")
	}
	if _, ok := enumerate("octo-no-such-title-8f3a", true, 0); ok {
		t.Fatal("title pass matched a nonsense name")
	}
	if _, err := windowForPid(1); err == nil {
		t.Fatal("pid 1 has no top-level window")
	}
}
