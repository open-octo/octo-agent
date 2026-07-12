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
	// A pre-existing .zshrc with the user's own PATH line and a STALE octo line
	// pointing at a long-gone location (mirrors the real bug on upgrade).
	stale := `export PATH="/opt/homebrew/bin:$PATH"
export PATH="` + home + `/Library/Application Support/octo/bin:$PATH"  ` + octoInstallerMarker + "\n"
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pre-create .bash_profile so it's a write target (it's only touched when it
	// already exists — see the no-create case below).
	if err := os.WriteFile(filepath.Join(home, ".bash_profile"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, ".local", "bin")
	ensureDirOnPath(home, dir)
	ensureDirOnPath(home, dir) // idempotent: a second launch must not duplicate

	want := `export PATH="` + dir + `:$PATH"  ` + octoInstallerMarker
	// .zshrc/.zprofile/.profile are always written; .bash_profile because it
	// existed here.
	for _, name := range []string{".zshrc", ".zprofile", ".profile", ".bash_profile"} {
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

// TestEnsureDirOnPath_DoesNotCreateBashProfile guards the footgun: creating
// ~/.bash_profile when it's absent makes bash login shells stop reading
// ~/.profile, silently shadowing the user's setup. When the user has no
// .bash_profile, we must not create one — .profile covers bash login instead.
func TestEnsureDirOnPath_DoesNotCreateBashProfile(t *testing.T) {
	home := t.TempDir()
	ensureDirOnPath(home, filepath.Join(home, ".local", "bin"))

	if _, err := os.Stat(filepath.Join(home, ".bash_profile")); !os.IsNotExist(err) {
		t.Errorf(".bash_profile should not be created when absent (err=%v)", err)
	}
	// .profile is still written, so bash login shells reach octo through it.
	if _, err := os.Stat(filepath.Join(home, ".profile")); err != nil {
		t.Errorf(".profile should have been written: %v", err)
	}
}
