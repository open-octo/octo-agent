//go:build !darwin

package main

// petCursor has no implementation away from macOS, so the pointer loop checks
// ok once and gives up, leaving the pet window solid — the same behaviour it
// had before shape-aware pass-through existed, rather than a half-working
// version.
//
// Windows could read the cursor (GetCursorPos) and once did, but the
// pass-through it fed is deliberately off there: flipping IgnoreMouseEvents on
// a transparent WebView2 window makes Wails add WS_EX_LAYERED to it, and a
// layered window paints the page's transparent pixels as an opaque white
// sheet — the octopus on a white card, which is worse than its empty corners
// swallowing a click. The style bit is never removed once set, so the loop
// cannot simply flip it back either. See wailsapp/wails#6088.
func petCursor() (x int, y int, ok bool) { return 0, 0, false }
