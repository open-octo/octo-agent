//go:build darwin && cgo

package computer

import "testing"

func TestMacKeyCode(t *testing.T) {
	cases := map[string]uint16{
		"enter": 36, "tab": 48, "escape": 53, "f5": 96,
		"backspace": 51, "delete": 117,
		"c": 8, "s": 1, "/": 44, "`": 50,
	}
	for key, want := range cases {
		got, ok := macKeyCode(key)
		if !ok || got != want {
			t.Errorf("macKeyCode(%q) = (%d, %v), want (%d, true)", key, got, ok, want)
		}
	}
	if _, ok := macKeyCode("中"); ok {
		t.Error("non-ASCII key must not resolve")
	}
}
