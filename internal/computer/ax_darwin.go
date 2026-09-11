//go:build darwin && cgo

package computer

/*
#include "helpers.h"
*/
import "C"

import (
	"fmt"
	"strconv"
	"strings"
	"unsafe"
)

const (
	// axMaxChildren caps fan-out per node, axMaxTotal caps the whole digest —
	// a runaway tree (browsers, Electron) must not turn one dump into a
	// minute of mach IPC.
	axMaxChildren = 200
	axMaxTotal    = 2000
)

func axString(el C.AXUIElementRef, attr C.CFStringRef) string {
	cs := C.octoAXCopyString(el, attr)
	if cs == nil {
		return ""
	}
	s := C.GoString(cs)
	C.free(unsafe.Pointer(cs))
	return s
}

// axRoots returns the app's windows plus its menu bar as traversal roots.
// The returned slices share ownership: every array/element here is +1 and
// released by the caller of axRoots.
func axRoots(app C.AXUIElementRef, withMenuBar bool) (arrays []C.CFArrayRef, singles []C.AXUIElementRef) {
	if wins := C.octoAXCopyArray(app, C.kAXWindowsAttribute); wins != 0 {
		arrays = append(arrays, wins)
	}
	if withMenuBar {
		if mb := C.octoAXCopyElement(app, C.kAXMenuBarAttribute); mb != 0 {
			singles = append(singles, mb)
		}
	}
	return arrays, singles
}

func axTree(pid, maxDepth int) ([]AXElement, error) {
	if maxDepth <= 0 {
		maxDepth = 12
	}
	app := C.octoAXApp(C.int(pid))
	if app == 0 {
		return nil, fmt.Errorf("computer: cannot create AX handle for pid %d", pid)
	}
	defer C.CFRelease(C.CFTypeRef(app))

	out := make([]AXElement, 0, 256)
	var walk func(el C.AXUIElementRef, depth int)
	walk = func(el C.AXUIElementRef, depth int) {
		if depth > maxDepth || len(out) >= axMaxTotal {
			return
		}
		e := AXElement{Depth: depth}
		e.Role = axString(el, C.kAXRoleAttribute)
		e.Subrole = axString(el, C.kAXSubroleAttribute)
		e.Title = axString(el, C.kAXTitleAttribute)
		e.Description = axString(el, C.kAXDescriptionAttribute)
		e.Value = axString(el, C.kAXValueAttribute)
		var x, y, w, h C.double
		if C.octoAXFrame(el, &x, &y, &w, &h) == 0 {
			e.X, e.Y, e.W, e.H = float64(x), float64(y), float64(w), float64(h)
		}
		out = append(out, e)

		kids := C.octoAXCopyArray(el, C.kAXChildrenAttribute)
		if kids == 0 {
			return
		}
		defer C.CFRelease(C.CFTypeRef(kids))
		n := int(C.CFArrayGetCount(kids))
		for i := 0; i < n && i < axMaxChildren && len(out) < axMaxTotal; i++ {
			// Array-owned reference: valid until kids is released, no extra
			// retain needed for the recursive call.
			walk(C.AXUIElementRef(C.CFArrayGetValueAtIndex(kids, C.CFIndex(i))), depth+1)
		}
	}

	arrays, singles := axRoots(app, true)
	for _, arr := range arrays {
		n := int(C.CFArrayGetCount(arr))
		for i := 0; i < n && len(out) < axMaxTotal; i++ {
			walk(C.AXUIElementRef(C.CFArrayGetValueAtIndex(arr, C.CFIndex(i))), 0)
		}
		C.CFRelease(C.CFTypeRef(arr))
	}
	for _, el := range singles {
		walk(el, 0)
		C.CFRelease(C.CFTypeRef(el))
	}
	return out, nil
}

// axFind locates the first element matching role+contains (same rules as
// MatchAX). The returned element is +1-retained via the parent chain: callers
// must finish with it before axFind's internal releases... — so instead this
// axFindDo traverses windows + menu bar and performs action on the first
// match. Matching is two-pass: an exact label match wins over a substring
// match anywhere later in the tree (otherwise Apple-menu "系统设置…" beats
// the app menu's own "设置…" for contains="设置").
func axFindDo(pid int, role, contains string, action func(C.AXUIElementRef) error) error {
	app := C.octoAXApp(C.int(pid))
	if app == 0 {
		return fmt.Errorf("computer: cannot create AX handle for pid %d", pid)
	}
	defer C.CFRelease(C.CFTypeRef(app))

	var matched bool
	var actErr error
	visited := 0

	search := func(pred func(AXElement) bool) bool {
		var walk func(el C.AXUIElementRef, depth int) bool // true = stop
		walk = func(el C.AXUIElementRef, depth int) bool {
			if matched || visited >= axMaxTotal {
				return true
			}
			visited++
			e := AXElement{
				Role:        axString(el, C.kAXRoleAttribute),
				Title:       axString(el, C.kAXTitleAttribute),
				Description: axString(el, C.kAXDescriptionAttribute),
				Value:       axString(el, C.kAXValueAttribute),
			}
			if pred(e) {
				matched = true
				actErr = action(el)
				return true
			}
			if depth >= 15 {
				return false
			}
			kids := C.octoAXCopyArray(el, C.kAXChildrenAttribute)
			if kids == 0 {
				return false
			}
			defer C.CFRelease(C.CFTypeRef(kids))
			n := int(C.CFArrayGetCount(kids))
			for i := 0; i < n && i < axMaxChildren; i++ {
				if walk(C.AXUIElementRef(C.CFArrayGetValueAtIndex(kids, C.CFIndex(i))), depth+1) {
					return true
				}
			}
			return false
		}

		arrays, singles := axRoots(app, true)
		for _, arr := range arrays {
			n := int(C.CFArrayGetCount(arr))
			for i := 0; i < n; i++ {
				if walk(C.AXUIElementRef(C.CFArrayGetValueAtIndex(arr, C.CFIndex(i))), 0) {
					break
				}
			}
			C.CFRelease(C.CFTypeRef(arr))
		}
		stopped := false
		for _, el := range singles {
			if !stopped {
				stopped = walk(el, 0)
			}
			C.CFRelease(C.CFTypeRef(el))
		}
		return matched
	}

	c := strings.TrimSpace(contains)
	if !search(func(e AXElement) bool { return MatchAXExact(e, role, c) }) {
		search(func(e AXElement) bool { return MatchAX(e, role, c) })
	}
	if !matched {
		return fmt.Errorf("computer: no AX element matching role=%q contains=%q", role, contains)
	}
	return actErr
}

func axPress(pid int, role, contains string) error {
	return axFindDo(pid, role, contains, func(el C.AXUIElementRef) error {
		if rc := C.octoAXPress(el); rc != 0 {
			return fmt.Errorf("computer: AXPress failed (AXError %d)", int(rc))
		}
		return nil
	})
}

func axSetValue(pid int, role, contains, value string) error {
	return axFindDo(pid, role, contains, func(el C.AXUIElementRef) error {
		// Sliders/steppers take a number; everything else takes a string.
		if isAXValueRole(eRoleOf(el)) {
			if f, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
				if rc := C.octoAXSetDouble(el, C.double(f)); rc != 0 {
					return fmt.Errorf("computer: AXSetValue(%g) failed (AXError %d)", f, int(rc))
				}
				return nil
			}
		}
		cv := C.CString(value)
		defer C.free(unsafe.Pointer(cv))
		if rc := C.octoAXSetString(el, cv); rc != 0 {
			return fmt.Errorf("computer: AXSetValue(%q) failed (AXError %d)", value, int(rc))
		}
		return nil
	})
}

func eRoleOf(el C.AXUIElementRef) string {
	return axString(el, C.kAXRoleAttribute)
}

// isAXValueRole reports whether the role's value is numeric.
func isAXValueRole(role string) bool {
	switch role {
	case "AXSlider", "AXStepper", "AXIncrementor", "AXLevelIndicator":
		return true
	}
	return false
}
