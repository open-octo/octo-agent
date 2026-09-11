package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The tool ships dark behind tools.computer.enabled: unset must hide it from
// the model's tool list. setHome (overwrite_backup_test.go) redirects
// os.UserHomeDir — on Windows that's USERPROFILE, not HOME, so both are set.
func TestComputerTool_GatedOffByDefault(t *testing.T) {
	setHome(t) // no config file → default off
	for _, d := range DefaultTools() {
		if d.Name == "computer" {
			t.Fatal("computer tool must not be advertised when tools.computer.enabled is unset")
		}
	}
}

// With the switch on, the tool is advertised — on macOS only; elsewhere the
// switch is ignored because the substrate does not exist.
func TestComputerTool_AdvertisedWhenEnabled(t *testing.T) {
	home := setHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".octo"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "tools:\n  computer:\n    enabled: \"on\"\n"
	if err := os.WriteFile(filepath.Join(home, ".octo", "config.yml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range DefaultTools() {
		if d.Name == "computer" {
			found = true
		}
	}
	if runtime.GOOS != "darwin" {
		if found {
			t.Fatal("computer tool must not be advertised off-darwin even when tools.computer.enabled is on")
		}
		return
	}
	if !found {
		t.Fatal("computer tool should be advertised when tools.computer.enabled is on")
	}
}
