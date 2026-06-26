package protean

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Leihb/octo-agent/internal/config"
)

func TestBridgeDefaults(t *testing.T) {
	b := NewBridge(config.ProteanConfig{})
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got := b.Venv(); got != filepath.Join(home, defaultRelVenv) {
		t.Errorf("Venv() = %q, want %q", got, filepath.Join(home, defaultRelVenv))
	}
	if got := b.SkillsDir(); got != filepath.Join(home, defaultRelSkills) {
		t.Errorf("SkillsDir() = %q, want %q", got, filepath.Join(home, defaultRelSkills))
	}
}

func TestBridgeVenvOverride(t *testing.T) {
	b := NewBridge(config.ProteanConfig{VenvPath: "/opt/protean"})
	if got := b.Venv(); got != "/opt/protean" {
		t.Errorf("Venv() = %q, want %q", got, "/opt/protean")
	}
}

func TestBridgeHomeExpansion(t *testing.T) {
	b := NewBridge(config.ProteanConfig{VenvPath: "~/my-venv"})
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got := b.Venv(); got != filepath.Join(home, "my-venv") {
		t.Errorf("Venv() = %q, want %q", got, filepath.Join(home, "my-venv"))
	}
}
