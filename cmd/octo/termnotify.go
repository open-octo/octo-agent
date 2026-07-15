package main

import (
	"fmt"
	"os"
	"strings"
)

// OSC (Operating System Command) escape sequences for terminal integration.
// Ghostty, iTerm2, Kitty, WezTerm and most modern terminals interpret these
// to surface agent state in the desktop environment — tab title and completion
// notifications — so the user can tab away from a long turn and be pulled back
// when octo needs input.

const (
	// osc2 sets the terminal tab/window title (OSC 2;title BEL).
	osc2 = "\033]2;%s\007"

	// Ghostty desktop notification: OSC 777;notify;title;body BEL.
	oscNotifyGhostty = "\033]777;notify;%s;%s\007"
	// iTerm2 / WezTerm desktop notification: OSC 9;body BEL.
	oscNotifyITerm = "\033]9;%s\007"
	// Kitty desktop notification: OSC 99;i=1:d=0;body BEL.
	oscNotifyKitty = "\033]99;i=1:d=0;%s\007"
	// Terminal bell — audible fallback for terminals without OSC-notify support.
	bell = "\a"
)

// setTitleSeq returns the OSC 2 sequence that sets the terminal tab/window
// title. Terminals ignore it if they don't support OSC 2, so it's safe to
// unconditionally emit.
func setTitleSeq(title string) string {
	return fmt.Sprintf(osc2, sanitizeOSC(title))
}

// notifySeq returns a desktop-notification sequence appropriate for the
// running terminal. It keys off TERM_PROGRAM, which Ghostty, iTerm2, Kitty and
// WezTerm all set; unknown / unsupported terminals (VS Code integrated,
// Windows Terminal, Terminal.app, etc.) get a terminal bell the user can
// actually hear instead of a silently-dropped OSC.
func notifySeq(title, body string) string {
	switch strings.TrimSpace(os.Getenv("TERM_PROGRAM")) {
	case "ghostty":
		return fmt.Sprintf(oscNotifyGhostty, sanitizeOSC(title), sanitizeOSC(body))
	case "iTerm.app", "WezTerm":
		return fmt.Sprintf(oscNotifyITerm, sanitizeOSC(body))
	case "kitty":
		return fmt.Sprintf(oscNotifyKitty, sanitizeOSC(body))
	default:
		return bell
	}
}

// sanitizeOSC strips BEL/ST and other control bytes out of a value destined for
// an OSC payload, so a malicious session title can't inject a premature
// sequence terminator and escape the OSC context.
func sanitizeOSC(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return -1
		}
		if r == '\\' {
			return -1
		}
		return r
	}, s)
}
