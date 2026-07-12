package main

import "testing"

func TestShouldSeedOcto(t *testing.T) {
	tests := []struct {
		name         string
		targetExists bool
		seededVer    string
		curVer       string
		want         bool
	}{
		{"fresh install seeds", false, "", "1.13.0", true},
		{"fresh install ignores stale record", false, "1.12.0", "1.13.0", true},
		{"user's own octo left untouched", true, "", "1.13.0", false},
		{"ours and current is a no-op", true, "1.13.0", "1.13.0", false},
		{"ours but stale refreshes on upgrade", true, "1.12.0", "1.13.0", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldSeedOcto(tt.targetExists, tt.seededVer, tt.curVer); got != tt.want {
				t.Errorf("shouldSeedOcto(%v, %q, %q) = %v, want %v",
					tt.targetExists, tt.seededVer, tt.curVer, got, tt.want)
			}
		})
	}
}
