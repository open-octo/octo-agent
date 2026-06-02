//go:build linux

package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// captureClipboardImage reads an image from the Linux clipboard, trying
// Wayland (wl-paste) first, then X11 (xclip). Both are common but neither is
// guaranteed installed, so a missing-tool case returns an actionable message.
// Whatever image type the clipboard offers (PNG, JPEG…) is returned as-is.
func captureClipboardImage() (data []byte, mime string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, hasWlPaste := lookPath("wl-paste")
	_, hasXclip := lookPath("xclip")
	if !hasWlPaste && !hasXclip {
		return nil, "", fmt.Errorf("clipboard image paste needs wl-clipboard (Wayland) or xclip (X11) installed")
	}

	if hasWlPaste {
		if d, m, ok := wlPasteImage(ctx); ok {
			return d, m, nil
		}
	}
	if hasXclip {
		// xclip can't easily enumerate image targets, so probe the common ones.
		for _, m := range []string{"image/png", "image/jpeg"} {
			out, e := exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", m, "-o").Output()
			if e == nil && len(out) > 0 {
				return out, m, nil
			}
		}
	}
	return nil, "", fmt.Errorf("no image in clipboard")
}

// wlPasteImage asks wl-paste which types the clipboard offers, picks the first
// image/* type, and pastes it. ok is false when there's no image type or the
// paste fails.
func wlPasteImage(ctx context.Context) (data []byte, mime string, ok bool) {
	typesOut, err := exec.CommandContext(ctx, "wl-paste", "--list-types").Output()
	if err != nil {
		return nil, "", false
	}
	for _, line := range strings.Fields(string(typesOut)) {
		if strings.HasPrefix(line, "image/") {
			mime = line
			break
		}
	}
	if mime == "" {
		return nil, "", false
	}
	out, err := exec.CommandContext(ctx, "wl-paste", "--no-newline", "--type", mime).Output()
	if err != nil || len(out) == 0 {
		return nil, "", false
	}
	return out, mime, true
}

// lookPath reports whether a binary is on PATH.
func lookPath(name string) (string, bool) {
	p, err := exec.LookPath(name)
	return p, err == nil
}
