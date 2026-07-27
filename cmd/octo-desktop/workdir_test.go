package main

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestHomeIfRootLaunch(t *testing.T) {
	// Real per-platform shapes: homeIfRootLaunch detects a root as "a path
	// that is its own parent" via filepath.Dir, whose separator handling is
	// platform-specific — POSIX "/" is not a root on Windows ("C:\" is).
	root, home := "/", "/Users/alice"
	if runtime.GOOS == "windows" {
		root, home = `C:\`, `C:\Users\alice`
	}
	cases := []struct {
		name string
		wd   string
		home string
		want string
	}{
		{"gui launch at filesystem root", root, home, home},
		{"getwd failed (empty)", "", home, home},
		{"terminal launch in a project dir", filepath.Join(home, "proj"), home, ""},
		{"home itself is fine", home, home, ""},
		{"no home resolved leaves cwd untouched", root, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := homeIfRootLaunch(tc.wd, tc.home); got != tc.want {
				t.Errorf("homeIfRootLaunch(%q, %q) = %q, want %q", tc.wd, tc.home, got, tc.want)
			}
		})
	}
}
