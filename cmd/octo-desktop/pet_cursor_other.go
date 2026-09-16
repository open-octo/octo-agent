//go:build !darwin && !windows

package main

// petCursor has no implementation on this platform. The pointer loop checks ok
// once and gives up, leaving the pet window solid — the same behaviour it had
// before shape-aware pass-through existed, rather than a half-working version.
func petCursor() (x int, y int, ok bool) { return 0, 0, false }
