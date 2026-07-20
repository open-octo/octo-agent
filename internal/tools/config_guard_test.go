package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/config"
)

func writeCfg(t *testing.T, body string) {
	t.Helper()
	p, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConfigGuard_ValidateConfigFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// Malformed YAML → the "did not take effect" parse warning.
	writeCfg(t, "models: [oops\n")
	if msg := validateConfigFile(); !strings.Contains(msg, "no longer parses") {
		t.Errorf("broken YAML: want a parse warning, got %q", msg)
	}

	// Parses, but Default dangles → the semantic warning. PR5: Default is a
	// composite id; "ghost::gpt-4o" points at a non-existent endpoint.
	writeCfg(t, "endpoints:\n  - id: ep-a\n    provider: openai\n    models:\n      - model: gpt-4o\ndefault: ghost::gpt-4o\n")
	if msg := validateConfigFile(); !strings.Contains(msg, "problems") || !strings.Contains(msg, "default") {
		t.Errorf("dangling Default: want a semantic warning, got %q", msg)
	}

	// Valid → no warning.
	writeCfg(t, "endpoints:\n  - id: ep-a\n    provider: openai\n    models:\n      - model: gpt-4o\ndefault: ep-a::gpt-4o\n")
	if msg := validateConfigFile(); msg != "" {
		t.Errorf("valid config: want no warning, got %q", msg)
	}
}

func TestConfigGuard_TouchedConfigFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	cfgPath, _ := config.Path()

	cases := []struct {
		name  string
		tool  string
		input map[string]any
		want  bool
	}{
		{"edit_file abs path", "edit_file", map[string]any{"path": cfgPath}, true},
		{"write_file ~ path", "write_file", map[string]any{"path": "~/.octo/config.yml"}, true},
		{"edit_file other file", "edit_file", map[string]any{"path": "/tmp/other.yml"}, false},
		{"terminal touches config", "terminal", map[string]any{"command": "sed -i '' s/x/y/ ~/.octo/config.yml"}, true},
		{"terminal unrelated", "terminal", map[string]any{"command": "go test ./..."}, false},
		{"terminal other project config.yml", "terminal", map[string]any{"command": "cat ./project/config.yml"}, false},
		{"read_file is not a write", "read_file", map[string]any{"path": cfgPath}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := touchedConfigFile(c.tool, c.input); got != c.want {
				t.Errorf("touchedConfigFile(%s) = %v, want %v", c.tool, got, c.want)
			}
		})
	}
}
