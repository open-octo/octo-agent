//go:build !darwin

package main

import "unsafe"

// petAcceptFirstMouse is a no-op away from macOS: the "first click only
// activates the window" behaviour it works around is an AppKit rule.
func petAcceptFirstMouse(nsWindow unsafe.Pointer) {}
