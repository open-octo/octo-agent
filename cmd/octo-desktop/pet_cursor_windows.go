//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	petUser32           = syscall.NewLazyDLL("user32.dll")
	petProcGetCursorPos = petUser32.NewProc("GetCursorPos")
)

// petWinPoint mirrors the Win32 POINT struct GetCursorPos fills in.
type petWinPoint struct{ X, Y int32 }

// petCursor returns the cursor position in the same coordinate space as
// WebviewWindow.Bounds(). Win32 already reports it from the top-left, so no
// flip is needed here (unlike the darwin path). Kept CGO-free deliberately —
// the desktop shell's Windows build has no other reason to need a C toolchain.
func petCursor() (x int, y int, ok bool) {
	var p petWinPoint
	r, _, _ := petProcGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	if r == 0 {
		return 0, 0, false
	}
	return int(p.X), int(p.Y), true
}
