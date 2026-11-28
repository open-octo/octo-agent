package tools

import (
	"strings"
	"testing"
)

// The missing-path error must tell the model what it actually sent, so a
// wrong key name (file_path) is fixed on the next call instead of retried.
func TestRequiredPath(t *testing.T) {
	if p, err := requiredPath("edit_file", map[string]any{"path": " a.go "}); err != nil || p != " a.go " {
		t.Errorf("present path: p=%q err=%v (must be returned untrimmed)", p, err)
	}

	_, err := requiredPath("edit_file", map[string]any{"file_path": "a.go", "old_string": "x", "new_string": "y"})
	if err == nil {
		t.Fatal("expected error when path is missing")
	}
	for _, want := range []string{"edit_file: path is required", `named "path"`, "received keys: file_path, new_string, old_string"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err.Error(), want)
		}
	}

	_, err = requiredPath("read_file", map[string]any{"path": "   "})
	if err == nil || !strings.Contains(err.Error(), "received keys: path") {
		t.Errorf("blank path should still list the received keys: %v", err)
	}

	_, err = requiredPath("write_file", nil)
	if err == nil || !strings.Contains(err.Error(), "no arguments were received") {
		t.Errorf("nil input: %v", err)
	}
}

// Models stringify scalars often enough that the arg readers must accept it —
// otherwise the value is silently replaced by the default and the tool acts on
// the wrong number (or a required field reads as missing).
func TestArgReaders_TolerateStringEncodedScalars(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   map[string]any
		want int
		ok   bool
	}{
		{"json number", map[string]any{"n": float64(7)}, 7, true},
		{"int", map[string]any{"n": 7}, 7, true},
		{"int64", map[string]any{"n": int64(7)}, 7, true},
		{"quoted", map[string]any{"n": "7"}, 7, true},
		{"quoted with spaces", map[string]any{"n": " 7 "}, 7, true},
		{"quoted negative", map[string]any{"n": "-5"}, -5, true},
		{"absent", map[string]any{}, 0, false},
		{"not a number", map[string]any{"n": "seven"}, 0, false},
		{"null", map[string]any{"n": nil}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := argInt(tc.in, "n"); got != tc.want || ok != tc.ok {
				t.Errorf("argInt = (%d, %v), want (%d, %v)", got, ok, tc.want, tc.ok)
			}
			if got := intArg(tc.in, "n", 42); tc.ok && got != tc.want {
				t.Errorf("intArg = %d, want %d", got, tc.want)
			} else if !tc.ok && got != 42 {
				t.Errorf("intArg should fall back to the default, got %d", got)
			}
		})
	}

	for _, tc := range []struct {
		in   map[string]any
		want bool
	}{
		{map[string]any{"b": true}, true},
		{map[string]any{"b": false}, false},
		{map[string]any{"b": "true"}, true},
		{map[string]any{"b": " TRUE "}, true},
		{map[string]any{"b": "false"}, false},
		{map[string]any{"b": float64(1)}, true},
		{map[string]any{"b": float64(0)}, false},
		{map[string]any{"b": int64(2)}, true},
		{map[string]any{"b": "yes"}, true},
		{map[string]any{}, false},
	} {
		if got := boolArg(tc.in, "b"); got != tc.want {
			t.Errorf("boolArg(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
