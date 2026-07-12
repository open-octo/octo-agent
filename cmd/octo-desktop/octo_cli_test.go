package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// TestEnsureDirOnPath_SelfHealsAndPreservesUserLines guards the PATH-writing
// that moved from the shell postinstall into the app: it must add the dir to
// each rc file exactly once, replace a stale octo-installer line (whatever dir
// it pointed at) rather than accumulate, and never touch the user's own lines.
func TestEnsureDirOnPath_SelfHealsAndPreservesUserLines(t *testing.T) {
	home := t.TempDir()
	// A pre-existing rc with the user's own PATH line and a STALE octo line
	// pointing at a long-gone location (mirrors the real bug on upgrade).
	stale := `export PATH="/opt/homebrew/bin:$PATH"
export PATH="` + home + `/Library/Application Support/octo/bin:$PATH"  ` + octoInstallerMarker + "\n"
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, ".local", "bin")
	ensureDirOnPath(home, dir)
	ensureDirOnPath(home, dir) // idempotent: a second launch must not duplicate

	want := `export PATH="` + dir + `:$PATH"  ` + octoInstallerMarker
	for _, name := range []string{".zshrc", ".zprofile", ".bash_profile", ".profile"} {
		data, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		body := string(data)
		if n := strings.Count(body, octoInstallerMarker); n != 1 {
			t.Errorf("%s: got %d marker lines, want 1:\n%s", name, n, body)
		}
		if !strings.Contains(body, want) {
			t.Errorf("%s: missing current PATH line %q:\n%s", name, want, body)
		}
		if strings.Contains(body, "Library/Application Support/octo/bin") {
			t.Errorf("%s: stale octo line not healed:\n%s", name, body)
		}
	}
	// The user's own line survives in the file that had it.
	zshrc, _ := os.ReadFile(filepath.Join(home, ".zshrc"))
	if !strings.Contains(string(zshrc), `/opt/homebrew/bin`) {
		t.Errorf(".zshrc: user's own PATH line was dropped:\n%s", zshrc)
	}
}
