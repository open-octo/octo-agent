//go:build !darwin && !linux && !windows

package main

import (
	"fmt"
	"runtime"
)

// captureClipboardImage covers the platforms without a dedicated clipboard
// backend (the BSDs, etc.). macOS, Linux, and Windows have their own files.
func captureClipboardImage() (data []byte, mime string, err error) {
	return nil, "", fmt.Errorf("clipboard image paste is not supported on %s", runtime.GOOS)
}
