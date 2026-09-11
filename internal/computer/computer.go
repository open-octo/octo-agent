// Package computer is the desktop computer-use substrate: screen capture plus
// input synthesis, so a vision-capable model can see the screen and act on it
// (screenshot → decide → click/type/key/scroll).
//
// The real implementation is macOS-only and lives behind CGO (Quartz
// CGEvent / CGWindowList); every other platform — including release builds,
// which compile with CGO_ENABLED=0 — gets a stub that reports
// ErrUnsupported, so the package always compiles and the tool degrades to a
// clear error instead of a build failure.
//
// Coordinates are in logical points of the main display, origin top-left —
// the same space CGEvent posts in and the space Screenshot's returned image
// is scaled to, so model coordinates can be used unconverted.
package computer

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnsupported marks every substrate call on a platform without a native
// implementation (non-macOS, or CGO disabled).
var ErrUnsupported = errors.New("computer-use is only supported on macOS builds with CGO enabled")

// Trusted reports whether the process may post input events to other apps
// (macOS Accessibility permission).
func Trusted() bool { return trusted() }

// ScreenCaptureAllowed reports whether the process may capture window
// contents (macOS Screen Recording permission).
func ScreenCaptureAllowed() bool { return screenCaptureAllowed() }

// RequestScreenCapture pops the macOS grant dialog for Screen Recording.
func RequestScreenCapture() { requestScreenCapture() }

// RequestAccessibility pops the macOS grant dialog for Accessibility.
func RequestAccessibility() { requestAccessibility() }

// ScreenSize returns the main display size in logical points — the coordinate
// space CGEvent posts in. Model coordinates (in sent-image pixels) map into it
// via ScreenSize/sentImageWidth.
func ScreenSize() (w, h float64, err error) { return screenSize() }

// Screenshot captures the main display and returns full-resolution PNG bytes
// (Retina displays are 2 logical points per pixel — do NOT assume image
// pixels equal ScreenSize).
func Screenshot() (png []byte, err error) { return screenshot() }

// MoveTo moves the cursor without clicking.
func MoveTo(x, y float64) error { return moveTo(x, y) }

// Click posts a click of button ("left" or "right") at (x, y); clicks=2 is a
// double click.
func Click(button string, x, y float64, clicks int) error {
	if button != "left" && button != "right" {
		return fmt.Errorf("computer: unknown button %q (left|right)", button)
	}
	if clicks < 1 || clicks > 2 {
		return fmt.Errorf("computer: clicks must be 1 or 2")
	}
	return click(button, x, y, clicks)
}

// Scroll posts a scroll-wheel event at the current cursor position. Positive
// dy scrolls down, positive dx scrolls right (natural units are lines).
func Scroll(dx, dy float64) error { return scroll(dx, dy) }

// TypeText types arbitrary Unicode text as keystrokes.
func TypeText(s string) error {
	if s == "" {
		return fmt.Errorf("computer: nothing to type")
	}
	return typeText(s)
}

// Press posts a key combination like "enter", "cmd+c", "ctrl+shift+tab".
func Press(keys string) error {
	kc, flags, err := parseCombo(keys)
	if err != nil {
		return err
	}
	return press(kc, flags)
}

// Window describes one on-screen window of a target app: its process, the
// window server id (for window-scoped capture), and its bounds in global
// logical points.
type Window struct {
	PID, ID    int
	X, Y, W, H float64
}

// FindWindow locates the front-most normal window of the app named owner
// (the process name as shown in the menu bar, e.g. "国际象棋" for Chess).
func FindWindow(owner string) (Window, error) { return findWindow(owner) }

// ClickPid delivers a click straight into the process's event queue at global
// logical coordinates — no cursor move, no focus change, so it can drive an
// app the user is not looking at. winID tags the owning window (AppKit drops
// untagged background mouse events). Whether a given app honours synthetic
// events while backgrounded is app-dependent (custom-drawn UIs may not).
func ClickPid(pid, winID int, button string, x, y float64, clicks int) error {
	if button != "left" && button != "right" {
		return fmt.Errorf("computer: unknown button %q (left|right)", button)
	}
	if clicks < 1 || clicks > 2 {
		return fmt.Errorf("computer: clicks must be 1 or 2")
	}
	return clickPid(pid, winID, button, x, y, clicks)
}

// TypeTextPid delivers keystrokes straight into one process. Text input is
// the one channel background apps commonly accept via PostToPid.
func TypeTextPid(pid int, s string) error {
	if s == "" {
		return fmt.Errorf("computer: nothing to type")
	}
	return typeTextPid(pid, s)
}

// AXElement is one node of an app's accessibility-tree digest.
type AXElement struct {
	Role, Subrole, Title, Description, Value string
	X, Y, W, H                               float64
	Depth                                    int
}

// Label is the element's human-facing name: the first non-empty of title,
// description, value.
func (e AXElement) Label() string {
	for _, s := range []string{e.Title, e.Description, e.Value} {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// MatchAX reports whether e matches a semantic target: role must equal when
// given (empty = any), and contains must appear (case-insensitive) in the
// label or description.
func MatchAX(e AXElement, role, contains string) bool {
	if role != "" && !strings.EqualFold(e.Role, role) {
		return false
	}
	c := strings.ToLower(strings.TrimSpace(contains))
	if c == "" {
		return false
	}
	return strings.Contains(strings.ToLower(e.Label()), c) ||
		strings.Contains(strings.ToLower(e.Description), c)
}

// MatchAXExact is the exact-label variant: wins over substring matches when
// both exist (an app menu's own "设置…" should beat the Apple menu's
// "系统设置…").
func MatchAXExact(e AXElement, role, label string) bool {
	if role != "" && !strings.EqualFold(e.Role, role) {
		return false
	}
	l := strings.TrimSpace(label)
	if l == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(e.Label()), l)
}

// AXTree returns a depth-limited digest of the app's accessibility tree —
// windows plus the menu bar. Works on backgrounded apps.
func AXTree(pid, maxDepth int) ([]AXElement, error) { return axTree(pid, maxDepth) }

// AXPress finds the first element matching role+label (see MatchAX) and
// performs its press action. Works on backgrounded apps — no cursor, no
// focus change — which makes it the preferred operation channel wherever the
// target exposes a real AX tree.
func AXPress(pid int, role, contains string) error {
	if strings.TrimSpace(contains) == "" {
		return fmt.Errorf("computer: AXPress needs a label to match")
	}
	return axPress(pid, role, contains)
}

// AXSetValue sets the value of the first matching element: numeric for
// sliders/steppers, string for text fields.
func AXSetValue(pid int, role, contains, value string) error {
	if strings.TrimSpace(contains) == "" {
		return fmt.Errorf("computer: AXSetValue needs a label to match")
	}
	return axSetValue(pid, role, contains, value)
}

// modifier flag bits, mirroring CGEventFlags so parseCombo stays
// platform-neutral and testable.
const (
	flagShift uint64 = 1 << iota
	flagControl
	flagOption
	flagCommand
)

// keyName aliases to macOS virtual keycodes.
var keyCodes = map[string]uint16{
	"return": 36, "enter": 36,
	"tab": 48, "space": 49,
	"backspace": 51, "delete": 51, "forwarddelete": 117,
	"escape": 53, "esc": 53,
	"left": 123, "right": 124, "down": 125, "up": 126,
	"home": 115, "end": 119, "pageup": 116, "pagedown": 121,
	"f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96, "f6": 97,
	"f7": 98, "f8": 100, "f9": 101, "f10": 109, "f11": 103, "f12": 111,
}

var modifierNames = map[string]uint64{
	"shift": flagShift, "ctrl": flagControl, "control": flagControl,
	"alt": flagOption, "option": flagOption, "opt": flagOption,
	"cmd": flagCommand, "command": flagCommand, "meta": flagCommand, "super": flagCommand,
}

// parseCombo splits "cmd+shift+enter" into a keycode plus modifier flags.
// A single printable character ("a", "5") is mapped to its keycode so
// key and type overlap for the simple cases. Exactly one non-modifier key is
// required: keycode 0 is the letter "a", so a modifier-only combo like "cmd"
// would otherwise silently post ⌘A (select all) instead of failing.
func parseCombo(combo string) (keycode uint16, flags uint64, err error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(combo)), "+")
	keys := 0
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return 0, 0, fmt.Errorf("computer: malformed key combo %q", combo)
		}
		if f, ok := modifierNames[p]; ok {
			flags |= f
			continue
		}
		if kc, ok := keyCodes[p]; ok {
			keycode = kc
			keys++
			continue
		}
		if kc, ok := charKeyCode(p); ok {
			keycode = kc
			keys++
			continue
		}
		return 0, 0, fmt.Errorf("computer: unknown key %q in combo %q", p, combo)
	}
	switch {
	case keys == 0:
		return 0, 0, fmt.Errorf("computer: key combo %q has no key, only modifiers", combo)
	case keys > 1:
		return 0, 0, fmt.Errorf("computer: key combo %q names more than one key (use type for text)", combo)
	}
	return keycode, flags, nil
}

// charKeyCode maps a single printable ASCII character to the macOS keycode
// that produces it (unshifted).
func charKeyCode(s string) (uint16, bool) {
	if len(s) != 1 {
		return 0, false
	}
	letters := map[byte]uint16{
		'a': 0, 's': 1, 'd': 2, 'f': 3, 'h': 4, 'g': 5, 'z': 6, 'x': 7,
		'c': 8, 'v': 9, 'b': 11, 'q': 12, 'w': 13, 'e': 14, 'r': 15,
		'y': 16, 't': 17, '1': 18, '2': 19, '3': 20, '4': 21, '6': 22,
		'5': 23, '=': 24, '9': 25, '7': 26, '-': 27, '8': 28, '0': 29,
		']': 30, 'o': 31, 'u': 32, '[': 33, 'i': 34, 'p': 35, 'l': 37,
		'j': 38, '\'': 39, 'k': 40, ';': 41, '\\': 42, ',': 43, '/': 44,
		'n': 45, 'm': 46, '.': 47, '`': 50,
	}
	kc, ok := letters[s[0]]
	return kc, ok
}
