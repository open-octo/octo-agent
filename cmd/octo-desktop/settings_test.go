package main

import (
	"encoding/json"
	"testing"
)

// TestDesktopSettings_WindowGeometryRoundTrip guards the on-disk contract for
// the remembered window geometry. A typo'd json tag would silently reset the
// window to its default size on every launch — the exact kind of quiet
// regression this test exists to catch.
func TestDesktopSettings_WindowGeometryRoundTrip(t *testing.T) {
	in := desktopSettings{
		WindowWidth:     1600,
		WindowHeight:    1000,
		WindowMaximised: true,
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out desktopSettings
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.WindowWidth != in.WindowWidth || out.WindowHeight != in.WindowHeight {
		t.Errorf("size not preserved: got %dx%d, want %dx%d",
			out.WindowWidth, out.WindowHeight, in.WindowWidth, in.WindowHeight)
	}
	if out.WindowMaximised != in.WindowMaximised {
		t.Errorf("maximised not preserved: got %v, want %v", out.WindowMaximised, in.WindowMaximised)
	}
}

// TestDefaultDesktopSettings_NoGeometry documents that a fresh install carries
// zero geometry, which showWindowAt reads as "use the built-in default size".
func TestDefaultDesktopSettings_NoGeometry(t *testing.T) {
	s := defaultDesktopSettings()
	if s.WindowWidth != 0 || s.WindowHeight != 0 || s.WindowMaximised {
		t.Errorf("defaults should carry no saved geometry, got %+v", s)
	}
}
