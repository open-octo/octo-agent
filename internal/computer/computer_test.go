package computer

import (
	"errors"
	"testing"
)

func TestParseCombo(t *testing.T) {
	cases := []struct {
		in       string
		wantKey  string
		wantFlag uint64
		wantErr  bool
	}{
		{"enter", "enter", 0, false},
		{"Return", "enter", 0, false},
		{"cmd+c", "c", flagCommand, false},
		{"ctrl+shift+tab", "tab", flagControl | flagShift, false},
		{"cmd+shift+s", "s", flagCommand | flagShift, false},
		{"escape", "escape", 0, false},
		{"esc", "escape", 0, false},
		{"f5", "f5", 0, false},
		{"cmd+option+esc", "escape", flagCommand | flagOption, false},
		{"delete", "delete", 0, false},
		{"forwarddelete", "delete", 0, false},
		{"backspace", "backspace", 0, false},
		{"ctrl+/", "/", flagControl, false},
		{"", "", 0, true},
		{"cmd+", "", 0, true},
		{"cmd+banana", "", 0, true},
		// Modifier-only combos must fail: keycode zero is "a" on macOS, so
		// these would otherwise post ⌘A / shift+A.
		{"cmd", "", 0, true},
		{"shift", "", 0, true},
		{"ctrl+shift", "", 0, true},
		// Two keys in one combo is text, not a chord.
		{"a+b", "", 0, true},
		// Non-ASCII single characters are not keys.
		{"中", "", 0, true},
	}
	for _, tc := range cases {
		key, flags, err := parseCombo(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseCombo(%q): want error, got key=%q flags=%d", tc.in, key, flags)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseCombo(%q): %v", tc.in, err)
			continue
		}
		if key != tc.wantKey || flags != tc.wantFlag {
			t.Errorf("parseCombo(%q) = (%q, %d), want (%q, %d)", tc.in, key, flags, tc.wantKey, tc.wantFlag)
		}
	}
}

func TestClickValidation(t *testing.T) {
	if err := Click("middle", 0, 0, 1); err == nil {
		t.Error("unknown button should error before touching the substrate")
	}
	if err := Click("left", 0, 0, 3); err == nil {
		t.Error("clicks > 2 should error before touching the substrate")
	}
}

// Supported() must agree with the stub: a build reporting no substrate returns
// ErrUnsupported from every action. Callers (internal/tools) gate on this so a
// stub build blames the build, not a missing permission grant.
func TestSupportedMatchesSubstrate(t *testing.T) {
	if Supported() {
		// A real substrate may still refuse a specific call (a missing macOS
		// grant, no on-screen window), so only the stub direction is asserted.
		return
	}
	png, shotErr := Screenshot()
	_, _, sizeErr := ScreenSize()
	tree, treeErr := AXTree(0, 1)
	_, winErr := FindWindow("Finder")
	cases := []struct {
		name string
		err  error
	}{
		{"screenshot", shotErr},
		{"screenSize", sizeErr},
		{"moveTo", MoveTo(0, 0)},
		{"click", Click("left", 0, 0, 1)},
		{"scroll", Scroll(0, 1)},
		{"typeText", TypeText("x")},
		{"press", Press("enter")},
		{"axTree", treeErr},
		{"axPress", AXPress(0, "", "x")},
		{"axSetValue", AXSetValue(0, "", "x", "1")},
		{"findWindow", winErr},
	}
	if png != nil || tree != nil {
		t.Error("stub build returned data for screenshot/axTree")
	}
	for _, tc := range cases {
		if !errors.Is(tc.err, ErrUnsupported) {
			t.Errorf("%s on an unsupported build: err = %v, want ErrUnsupported", tc.name, tc.err)
		}
	}
}

func TestMatchAX(t *testing.T) {
	el := AXElement{Role: "AXButton", Title: "存储", Description: "保存文档"}
	cases := []struct {
		name           string
		el             AXElement
		role, contains string
		want           bool
	}{
		{"title match", el, "AXButton", "存", true},
		{"title match any role", el, "", "存储", true},
		{"case-insensitive role", el, "axbutton", "存", true},
		{"wrong role", el, "AXMenuItem", "存", false},
		{"description match", el, "", "保存", true},
		{"no match", el, "", "删除", false},
		{"empty contains never matches", el, "", "  ", false},
		{"value used as label", AXElement{Role: "AXStaticText", Value: "42"}, "", "42", true},
		// Windows UIA spells roles without the AX prefix; both spellings match.
		{"windows role vs AX role", AXElement{Role: "Button", Title: "存储"}, "AXButton", "存", true},
		{"AX role vs windows role", el, "Button", "存", true},
		{"wrong windows role", AXElement{Role: "MenuItem", Title: "存储"}, "AXButton", "存", false},
	}
	for _, tc := range cases {
		if got := MatchAX(tc.el, tc.role, tc.contains); got != tc.want {
			t.Errorf("%s: MatchAX(%+v, %q, %q) = %v, want %v", tc.name, tc.el, tc.role, tc.contains, got, tc.want)
		}
	}
}
