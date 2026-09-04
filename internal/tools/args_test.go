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
