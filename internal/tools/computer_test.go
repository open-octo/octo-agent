package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/computer"
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

// With the switch on, the tool is advertised — on macOS and Windows only;
// elsewhere the switch is ignored because the substrate does not exist. A
// macOS build without CGO still advertises (see computerPlatform): its actions
// fail with an ErrUnsupported that names the missing CGO.
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
	if !computerPlatform() {
		if found {
			t.Fatal("computer tool must not be advertised on a platform without a substrate even when tools.computer.enabled is on")
		}
		return
	}
	if !found {
		t.Fatal("computer tool should be advertised when tools.computer.enabled is on")
	}
}

// A build with no substrate must say so rather than blame a missing permission
// grant. The stub reports Trusted() and ScreenCaptureAllowed() false, so before
// requireSubstrate the permission gates fired first and told the user to grant
// Accessibility in System Settings — a switch that cannot help, because there
// is no implementation behind it (the case CGO-less darwin CLI builds shipped).
func TestComputerTool_UnsupportedBuildBlamesNoPermission(t *testing.T) {
	if computer.Supported() {
		t.Skip("build has a substrate; the stub path runs on the unsupported/CGO-less CI legs")
	}
	// Enough arguments that any action would get past its own operand checks.
	input := map[string]any{
		"action": "left_click", "x": 1.0, "y": 1.0, "text": "x",
		"key": "enter", "app": "Finder", "label": "OK", "role": "AXButton",
	}
	for _, action := range []string{
		"screenshot", "left_click", "right_click", "double_click", "mouse_move",
		"type", "key", "scroll", "ax_tree", "ax_press", "ax_set",
	} {
		input["action"] = action
		_, err := ComputerTool{}.Execute(context.Background(), "computer", input)
		if !errors.Is(err, computer.ErrUnsupported) {
			t.Errorf("%s: err = %v, want ErrUnsupported — a permission error here is the misleading case", action, err)
		}
	}
}
