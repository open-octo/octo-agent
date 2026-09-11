package computer

import "testing"

func TestParseCombo(t *testing.T) {
	cases := []struct {
		in       string
		wantCode uint16
		wantFlag uint64
		wantErr  bool
	}{
		{"enter", 36, 0, false},
		{"Return", 36, 0, false},
		{"cmd+c", 8, flagCommand, false},
		{"ctrl+shift+tab", 48, flagControl | flagShift, false},
		{"cmd+shift+s", 1, flagCommand | flagShift, false},
		{"escape", 53, 0, false},
		{"f5", 96, 0, false},
		{"cmd+option+esc", 53, flagCommand | flagOption, false},
		{"", 0, 0, true},
		{"cmd+", 0, 0, true},
		{"cmd+banana", 0, 0, true},
		// Modifier-only combos must fail: the zero keycode is "a", so these
		// would otherwise post ⌘A / shift+A.
		{"cmd", 0, 0, true},
		{"shift", 0, 0, true},
		{"ctrl+shift", 0, 0, true},
		// Two keys in one combo is text, not a chord.
		{"a+b", 0, 0, true},
	}
	for _, tc := range cases {
		kc, flags, err := parseCombo(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseCombo(%q): want error, got kc=%d flags=%d", tc.in, kc, flags)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseCombo(%q): %v", tc.in, err)
			continue
		}
		if kc != tc.wantCode || flags != tc.wantFlag {
			t.Errorf("parseCombo(%q) = (%d, %d), want (%d, %d)", tc.in, kc, flags, tc.wantCode, tc.wantFlag)
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
	}
	for _, tc := range cases {
		if got := MatchAX(tc.el, tc.role, tc.contains); got != tc.want {
			t.Errorf("%s: MatchAX(%+v, %q, %q) = %v, want %v", tc.name, tc.el, tc.role, tc.contains, got, tc.want)
		}
	}
}
