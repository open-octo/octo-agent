package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/computer"
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

// TestRenderAXTree_IDsAreTrueIndexNotDisplayCounter locks the one invariant
// an id-addressed ax_press/ax_set depends on: the printed "e<N>" must be the
// element's real position in the slice AXTree returned (what axActByIndex
// re-walks to), never a counter of how many lines were actually printed.
// axStructuralRoles filters some elements out of the text, so a naive
// "shown so far" counter would silently desync ids from real indices —
// this test would catch that regression even though it needs no AX/UIA
// substrate.
func TestRenderAXTree_IDsAreTrueIndexNotDisplayCounter(t *testing.T) {
	els := []computer.AXElement{
		{Role: "AXWindow", Title: "Untitled"},       // e0 - window, always shown
		{Role: "AXGroup", Title: ""},                // e1 - filtered: structural + unlabeled
		{Role: "AXTextField", Title: "", Value: ""}, // e2 - shown: not a noise role, even unlabeled
		{Role: "AXButton", Title: "Save"},           // e3 - shown
		{Role: "AXSplitGroup", Title: ""},           // e4 - filtered: structural + unlabeled
		{Role: "AXSlider", Title: "", Value: "0"},   // e5 - shown: Value counts as label
	}
	out := renderAXTree(els)

	for _, want := range []string{"e0 AXWindow", "e2 AXTextField", "e3 AXButton", "e5 AXSlider"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderAXTree output missing %q; got:\n%s", want, out)
		}
	}
	for _, mustNotAppearAsID := range []string{"e1 ", "e4 "} {
		if strings.Contains(out, mustNotAppearAsID) {
			t.Errorf("renderAXTree must not print a line for filtered element as %q; got:\n%s", mustNotAppearAsID, out)
		}
	}
	// The filtered elements (e1, e4) must not cause the surviving ones to be
	// renumbered — e.g. e2 must never print as "e1" just because one line
	// was skipped before it.
	if strings.Contains(out, "e1 AXTextField") || strings.Contains(out, "e2 AXButton") {
		t.Fatalf("ids were renumbered after filtering instead of keeping the true slice index; got:\n%s", out)
	}
	if !strings.Contains(out, "(4 of 6 elements shown") {
		t.Errorf("summary line should count 4 shown of 6 total; got:\n%s", out)
	}
}
