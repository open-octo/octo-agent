package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLogger_Log(t *testing.T) {
	dir := t.TempDir()
	l := &Logger{path: filepath.Join(dir, "audit.log")}

	l.Log("terminal", map[string]any{"command": "rm -rf /"}, "deny", "hardcoded guard")
	l.Log("write_file", map[string]any{"path": "/etc/passwd"}, "deny", "sensitive path")

	b, err := os.ReadFile(l.path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 log lines, got %d", len(lines))
	}

	var first Event
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("unmarshal first line: %v", err)
	}
	if first.Tool != "terminal" || first.Decision != "deny" || first.Reason != "hardcoded guard" {
		t.Errorf("first event mismatch: %+v", first)
	}
	if first.Timestamp == "" {
		t.Error("timestamp should be set")
	}
	if len(first.Input) == 0 {
		t.Error("input should be preserved")
	}

	var second Event
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("unmarshal second line: %v", err)
	}
	if second.Tool != "write_file" {
		t.Errorf("second event tool mismatch: %+v", second)
	}
}

func TestLogger_Concurrent(t *testing.T) {
	dir := t.TempDir()
	l := &Logger{path: filepath.Join(dir, "audit.log")}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.Log("terminal", map[string]any{"command": "ls"}, "allow", "")
		}()
	}
	wg.Wait()

	b, err := os.ReadFile(l.path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 100 {
		t.Fatalf("expected 100 lines, got %d", len(lines))
	}
}

func TestLogger_Nil(t *testing.T) {
	var l *Logger
	// Should not panic.
	l.Log("terminal", map[string]any{"command": "ls"}, "allow", "")
}

func TestLogger_NoPath(t *testing.T) {
	l := &Logger{path: ""}
	// Should not panic or write anywhere.
	l.Log("terminal", map[string]any{"command": "ls"}, "allow", "")
}

func TestLogger_InvalidDirectory(t *testing.T) {
	// Use a path whose parent cannot be created (a file with the same name as
	// the directory).
	file := filepath.Join(t.TempDir(), "parent")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := &Logger{path: filepath.Join(file, "audit.log")}
	// Should not panic; error is logged but not returned.
	l.Log("terminal", map[string]any{"command": "ls"}, "allow", "")
}
