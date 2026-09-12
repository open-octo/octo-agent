package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseElementID(t *testing.T) {
	cases := []struct {
		name    string
		input   map[string]any
		wantID  int
		wantHas bool
		wantErr bool
	}{
		{"absent", map[string]any{}, 0, false, false},
		{"nil value", map[string]any{"id": nil}, 0, false, false},
		{"e-prefixed string", map[string]any{"id": "e12"}, 12, true, false},
		{"upper E-prefixed string", map[string]any{"id": "E7"}, 7, true, false},
		{"bare numeric string", map[string]any{"id": "3"}, 3, true, false},
		{"whitespace padded", map[string]any{"id": " e5 "}, 5, true, false},
		{"json number (float64)", map[string]any{"id": float64(9)}, 9, true, false},
		{"plain int", map[string]any{"id": 4}, 4, true, false},
		{"garbage string", map[string]any{"id": "banana"}, 0, true, true},
		{"unsupported type", map[string]any{"id": true}, 0, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, has, err := parseElementID(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseElementID(%v): want error, got id=%d has=%v", tc.input, id, has)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseElementID(%v): %v", tc.input, err)
			}
			if id != tc.wantID || has != tc.wantHas {
				t.Fatalf("parseElementID(%v) = (%d, %v), want (%d, %v)", tc.input, id, has, tc.wantID, tc.wantHas)
			}
		})
	}
}

func TestActivateAppArg_NoopWithoutApp(t *testing.T) {
	// No "app" key at all must short-circuit before touching the substrate
	// (no Accessibility/window-list calls), so this must not error even
	// without the permission grants a real activation would need.
	if err := activateAppArg(map[string]any{}); err != nil {
		t.Fatalf("activateAppArg with no app must no-op, got: %v", err)
	}
	if err := activateAppArg(map[string]any{"app": ""}); err != nil {
		t.Fatalf("activateAppArg with empty app must no-op, got: %v", err)
	}
}

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
// elsewhere the switch is ignored because the substrate does not exist.
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
