// Package computer is the desktop computer-use substrate: screen capture plus
// input synthesis, so a vision-capable model can see the screen and act on it
// (screenshot → decide → click/type/key/scroll).
//
// Two real implementations exist. macOS lives behind CGO (Quartz CGEvent /
// CGWindowList / AXUIElement). Windows is pure Go over Win32 (SendInput, GDI
// capture) and UI Automation COM, so it ships in the CGO_ENABLED=0 release
// cross-builds unchanged. Every other target — and a macOS build without
// CGO — gets a stub that reports ErrUnsupported, so the package always
// compiles and the tool degrades to a clear error instead of a build failure.
//
// Coordinates are in the main display's native input space, origin top-left:
// logical points on macOS (what CGEvent posts in), physical pixels on Windows
// (the process opts into per-monitor DPI awareness so SendInput, GetSystemMetrics
// and the GDI capture all agree). Screenshot's pixel size may differ from
// ScreenSize (Retina), which is why callers map through ScreenSize/imageWidth.
package computer

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnsupported marks every substrate call on a platform without a native
// implementation (neither macOS-with-CGO nor Windows).
var ErrUnsupported = errors.New("computer-use is only supported on macOS builds with CGO enabled and on Windows")

// Trusted reports whether the process may post input events to other apps
// (macOS Accessibility permission; Windows has no such grant and reports true).
func Trusted() bool { return trusted() }

// ScreenCaptureAllowed reports whether the process may capture window
// contents (macOS Screen Recording permission; always true on Windows).
func ScreenCaptureAllowed() bool { return screenCaptureAllowed() }

// RequestScreenCapture pops the macOS grant dialog for Screen Recording
// (no-op elsewhere).
func RequestScreenCapture() { requestScreenCapture() }

// RequestAccessibility pops the macOS grant dialog for Accessibility (no-op
// elsewhere).
func RequestAccessibility() { requestAccessibility() }

// ScreenSize returns the main display size in the platform's input coordinate
// space (logical points on macOS, physical pixels on Windows). Model
// coordinates (in sent-image pixels) map into it via ScreenSize/sentImageWidth.
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
// "cmd" is the Command key on macOS and the Windows key on Windows; "ctrl"
// is Control on both.
func Press(keys string) error {
	key, flags, err := parseCombo(keys)
	if err != nil {
		return err
	}
	return press(key, flags)
}

// Window describes one on-screen window of a target app: its process, the
// window server id (for window-scoped capture), and its bounds in global
// logical points.
type Window struct {
	PID, ID    int
	X, Y, W, H float64
}

// ActivateApp brings pid's app to the foreground without moving the cursor —
// call it before a pixel-channel click/type/key targeting an app that might
// not already have focus. Octo's own window can otherwise hold focus and
// silently swallow the input; ClickPid/TypeTextPid's per-process delivery
// (below) was tried first and measured unreliable (dropped by both SwiftUI
// and custom-drawn views), so real focus is the fix that actually works.
func ActivateApp(pid int) error { return activateApp(pid) }

// FrontmostAppName reports the name of the app currently in the foreground,
// "" if it could not be determined — used to sanity-check that a type/key
// action actually landed in the intended app instead of Octo's own window.
func FrontmostAppName() string { return frontmostAppName() }

// FindWindow locates the front-most normal window of the app named owner:
// on macOS the process name as shown in the menu bar (e.g. "国际象棋" for
// Chess); on Windows the executable name without ".exe" or, failing that, a
// case-insensitive substring of a window title.
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

const (
	// axMaxChildren caps fan-out per node, axMaxTotal caps the whole digest —
	// a runaway tree (browsers, Electron) must not turn one dump into a
	// minute of accessibility IPC on either platform.
	axMaxChildren = 200
	axMaxTotal    = 2000
)

// AXElement is one node of an app's accessibility-tree digest. Role is the
// platform's role name — "AXButton" on macOS, "Button" on Windows UI
// Automation; MatchAX treats those two spellings as equal.
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

// normRole folds the two platforms' role spellings together: "AXButton"
// (macOS) and "Button" (Windows UIA) compare equal, case-insensitively.
func normRole(role string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(role)), "ax")
}

// SameRole reports whether two role spellings name the same role across
// platforms ("AXWindow" and "Window", any case).
func SameRole(a, b string) bool { return normRole(a) == normRole(b) }

func roleMatches(e AXElement, role string) bool {
	return role == "" || SameRole(e.Role, role)
}

// MatchAX reports whether e matches a semantic target: role must equal when
// given (empty = any; "AXButton" and "Button" are the same role), and
// contains must appear (case-insensitive) in the label or description.
func MatchAX(e AXElement, role, contains string) bool {
	if !roleMatches(e, role) {
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
	if !roleMatches(e, role) {
		return false
	}
	l := strings.TrimSpace(label)
	if l == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(e.Label()), l)
}

// AXTree returns a depth-limited digest of the app's accessibility tree —
// windows plus the menu bar. Works on backgrounded apps. Each element's
// position in the returned slice is its addressable id: AXPressByID and
// AXSetValueByID take that same 0-based index (under the same maxDepth) to
// act on an element ax_tree shows with an empty or duplicate label, which
// role+contains matching (AXPress/AXSetValue) cannot disambiguate.
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

// AXPressByID is AXPress's id-addressed counterpart: it presses the element
// at the given 0-based index into AXTree(pid, maxDepth)'s result, bypassing
// role/label matching entirely. Returns the matched element's digest so the
// caller can echo back what it actually pressed.
func AXPressByID(pid, maxDepth, id int) (AXElement, error) {
	if id < 0 {
		return AXElement{}, fmt.Errorf("computer: element id must be >= 0")
	}
	return axPressByID(pid, maxDepth, id)
}

// AXSetValue sets the value of the first matching element: numeric for
// sliders/steppers, string for text fields.
func AXSetValue(pid int, role, contains, value string) error {
	if strings.TrimSpace(contains) == "" {
		return fmt.Errorf("computer: AXSetValue needs a label to match")
	}
	return axSetValue(pid, role, contains, value)
}

// AXSetValueByID is AXSetValue's id-addressed counterpart; see AXPressByID.
func AXSetValueByID(pid, maxDepth, id int, value string) (AXElement, error) {
	if id < 0 {
		return AXElement{}, fmt.Errorf("computer: element id must be >= 0")
	}
	return axSetValueByID(pid, maxDepth, id, value)
}

// modifier flag bits, mirroring CGEventFlags so parseCombo stays
// platform-neutral and testable.
const (
	flagShift uint64 = 1 << iota
	flagControl
	flagOption
	flagCommand
)

// keyAliases maps every accepted spelling of a named key to its canonical
// name. Platform files map canonical names to virtual keycodes; a spelling
// missing here that is not a single printable ASCII character is an error.
// "delete" is the forward-delete key on both platforms; the key that erases
// backwards is "backspace".
var keyAliases = map[string]string{
	"return": "enter", "enter": "enter",
	"tab": "tab", "space": "space",
	"backspace": "backspace",
	"delete":    "delete", "forwarddelete": "delete",
	"escape": "escape", "esc": "escape",
	"left": "left", "right": "right", "down": "down", "up": "up",
	"home": "home", "end": "end", "pageup": "pageup", "pagedown": "pagedown",
	"f1": "f1", "f2": "f2", "f3": "f3", "f4": "f4", "f5": "f5", "f6": "f6",
	"f7": "f7", "f8": "f8", "f9": "f9", "f10": "f10", "f11": "f11", "f12": "f12",
}

var modifierNames = map[string]uint64{
	"shift": flagShift, "ctrl": flagControl, "control": flagControl,
	"alt": flagOption, "option": flagOption, "opt": flagOption,
	"cmd": flagCommand, "command": flagCommand, "meta": flagCommand, "super": flagCommand,
}

// parseCombo splits "cmd+shift+enter" into a canonical key name plus modifier
// flags. A single printable ASCII character ("a", "5", "/") is its own key
// name so key and type overlap for the simple cases. Exactly one non-modifier
// key is required: a modifier-only combo like "cmd" must fail rather than
// fall through to whatever keycode zero means on the platform (the letter "a"
// on macOS, so "cmd" would have posted ⌘A).
func parseCombo(combo string) (key string, flags uint64, err error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(combo)), "+")
	keys := 0
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return "", 0, fmt.Errorf("computer: malformed key combo %q", combo)
		}
		if f, ok := modifierNames[p]; ok {
			flags |= f
			continue
		}
		if name, ok := keyAliases[p]; ok {
			key = name
			keys++
			continue
		}
		if isPrintableASCII(p) {
			key = p
			keys++
			continue
		}
		return "", 0, fmt.Errorf("computer: unknown key %q in combo %q", p, combo)
	}
	switch {
	case keys == 0:
		return "", 0, fmt.Errorf("computer: key combo %q has no key, only modifiers", combo)
	case keys > 1:
		return "", 0, fmt.Errorf("computer: key combo %q names more than one key (use type for text)", combo)
	}
	return key, flags, nil
}

// isPrintableASCII reports whether s is exactly one printable, non-space
// ASCII character — the "type a single key by its character" case.
func isPrintableASCII(s string) bool {
	return len(s) == 1 && s[0] > ' ' && s[0] <= '~'
}
