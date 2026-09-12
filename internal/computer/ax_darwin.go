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

func axString(el C.AXUIElementRef, attr C.CFStringRef) string {
	cs := C.octoAXCopyString(el, attr)
	if cs == nil {
		return ""
	}
	s := C.GoString(cs)
	C.free(unsafe.Pointer(cs))
	return s
}

// axRoots returns the app's traversal roots: windows, its menu bar, or both,
// selected independently so a menu-bar dump doesn't also drag in the window
// tree (and vice versa) — the two are unrelated in size and in what the
// caller is looking for. The returned slices share ownership: every
// array/element here is +1 and released by the caller of axRoots.
func axRoots(app C.AXUIElementRef, includeWindows, includeMenuBar bool) (arrays []C.CFArrayRef, singles []C.AXUIElementRef) {
	if includeWindows {
		if wins := C.octoAXCopyArray(app, C.kAXWindowsAttribute); wins != 0 {
			arrays = append(arrays, wins)
		}
	}
	if includeMenuBar {
		if mb := C.octoAXCopyElement(app, C.kAXMenuBarAttribute); mb != 0 {
			singles = append(singles, mb)
		}
	}
	return arrays, singles
}

// axWalk performs the same depth-first traversal axTree renders — either the
// app's windows (menuBar=false, the default: the menu bar on a AX-chatty app
// can be hundreds of AXMenuItem entries the model will never click, and
// dumping it by default wastes most of a digest's context budget on noise)
// or, when menuBar is true, the menu bar ALONE (not the windows — a caller
// who wants "文件 > 另存为…" asks for the menu bar specifically instead of
// paying for both trees at once). Depth- and fan-out-capped; calls visit for
// every element with its 0-based traversal index while the raw element is
// still valid. visit returning false stops the whole walk immediately — used
// by axActByIndex to act on one element without paying for the rest of a
// possibly-large tree.
func axWalk(pid, maxDepth int, menuBar bool, visit func(index int, el C.AXUIElementRef, e AXElement) bool) error {
	if maxDepth <= 0 {
		maxDepth = 12
	}
	app := C.octoAXApp(C.int(pid))
	if app == 0 {
		return fmt.Errorf("computer: cannot create AX handle for pid %d", pid)
	}
	defer C.CFRelease(C.CFTypeRef(app))

	index := 0
	stop := false
	var walk func(el C.AXUIElementRef, depth int)
	walk = func(el C.AXUIElementRef, depth int) {
		if stop || depth > maxDepth || index >= axMaxTotal {
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
		if !visit(index, el, e) {
			stop = true
			return
		}
		index++

		kids := C.octoAXCopyArray(el, C.kAXChildrenAttribute)
		if kids == 0 {
			return
		}
		defer C.CFRelease(C.CFTypeRef(kids))
		n := int(C.CFArrayGetCount(kids))
		for i := 0; i < n && i < axMaxChildren && !stop; i++ {
			// Array-owned reference: valid until kids is released, no extra
			// retain needed for the recursive call.
			walk(C.AXUIElementRef(C.CFArrayGetValueAtIndex(kids, C.CFIndex(i))), depth+1)
		}
	}

	arrays, singles := axRoots(app, !menuBar, menuBar)
	for _, arr := range arrays {
		n := int(C.CFArrayGetCount(arr))
		for i := 0; i < n && !stop; i++ {
			walk(C.AXUIElementRef(C.CFArrayGetValueAtIndex(arr, C.CFIndex(i))), 0)
		}
		C.CFRelease(C.CFTypeRef(arr))
	}
	for _, el := range singles {
		if !stop {
			walk(el, 0)
		}
		C.CFRelease(C.CFTypeRef(el))
	}
	return nil
}

func axTree(pid, maxDepth int, menuBar bool) ([]AXElement, error) {
	out := make([]AXElement, 0, 256)
	err := axWalk(pid, maxDepth, menuBar, func(_ int, _ C.AXUIElementRef, e AXElement) bool {
		out = append(out, e)
		return true
	})
	return out, err
}

// axActByIndex performs action on the element at the given 0-based
// traversal index — the same numbering axTree's returned slice uses, under
// the same pid + maxDepth. This is the id-addressed counterpart to
// axFindDo's role+label matching, for elements ax_tree shows with an empty
// or duplicate label (axFindDo can't disambiguate those by contains/role).
// Returns the matched element's digest (role/label/frame) alongside any
// error so the caller can echo back what it actually hit — the one signal
// that an index-addressed action landed on the right widget.
func axActByIndex(pid, maxDepth int, menuBar bool, target int, action func(C.AXUIElementRef) error) (AXElement, error) {
	var found bool
	var matched AXElement
	var actErr error
	err := axWalk(pid, maxDepth, menuBar, func(i int, el C.AXUIElementRef, e AXElement) bool {
		if i == target {
			found = true
			matched = e
			actErr = action(el)
			return false
		}
		return true
	})
	if err != nil {
		return AXElement{}, err
	}
	if !found {
		return AXElement{}, fmt.Errorf("computer: no element with id e%d (re-dump ax_tree — the tree may have changed, or max_depth differs from the dump that produced this id)", target)
	}
	return matched, actErr
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
		// Each pass gets the full budget: the exact pass exhausting it must
		// not silently skip the substring fallback on a large tree.
		visited = 0
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

		// Label/role matching (ax_press/ax_set without an id) keeps searching
		// windows AND the menu bar by default — unlike the dumped digest, this
		// never reaches the model's context window, so there is no size
		// pressure pushing the menu bar out of scope here.
		arrays, singles := axRoots(app, true, true)
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

// doAXPress performs an already-located element's press action; shared by
// the label-matched (axPress) and id-addressed (axPressByID) paths.
func doAXPress(el C.AXUIElementRef) error {
	if rc := C.octoAXPress(el); rc != 0 {
		return fmt.Errorf("computer: AXPress failed (AXError %d)", int(rc))
	}
	return nil
}

func axPress(pid int, role, contains string) error {
	return axFindDo(pid, role, contains, doAXPress)
}

func axPressByID(pid, maxDepth int, menuBar bool, id int) (AXElement, error) {
	return axActByIndex(pid, maxDepth, menuBar, id, doAXPress)
}

// doAXSetValue is axSetValue/axSetValueByID's shared element-level action:
// sliders/steppers take a number, everything else takes a string.
func doAXSetValue(el C.AXUIElementRef, value string) error {
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
}

func axSetValue(pid int, role, contains, value string) error {
	return axFindDo(pid, role, contains, func(el C.AXUIElementRef) error {
		return doAXSetValue(el, value)
	})
}

func axSetValueByID(pid, maxDepth int, menuBar bool, id int, value string) (AXElement, error) {
	return axActByIndex(pid, maxDepth, menuBar, id, func(el C.AXUIElementRef) error {
		return doAXSetValue(el, value)
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
