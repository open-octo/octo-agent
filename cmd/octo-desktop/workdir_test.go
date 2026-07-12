package main

import "testing"

func TestHomeIfRootLaunch(t *testing.T) {
	const home = "/Users/alice"
	cases := []struct {
		name string
		wd   string
		home string
		want string
	}{
		{"finder launch at root", "/", home, home},
		{"getwd failed (empty)", "", home, home},
		{"terminal launch in a project dir", "/Users/alice/proj", home, ""},
		{"home itself is fine", home, home, ""},
		{"no home resolved leaves cwd untouched", "/", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := homeIfRootLaunch(tc.wd, tc.home); got != tc.want {
				t.Errorf("homeIfRootLaunch(%q, %q) = %q, want %q", tc.wd, tc.home, got, tc.want)
			}
		})
	}
}
