package rgembed

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPath_SystemRG(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not on PATH — skipping system-rg test")
	}
	p, err := Path()
	if err != nil {
		t.Fatalf("Path() error: %v", err)
	}
	if p != "rg" {
		t.Errorf("Path() = %q, want %q", p, "rg")
	}
}

func TestPath_ExtractEmbedded(t *testing.T) {
	if len(embeddedRG) == 0 {
		t.Skip("embeddedRG is nil — build with -tags=embedrg to run this test")
	}

	// Extract into a throwaway home. A bare `go test` leaves version at
	// "unknown" (it is only injected via -ldflags), so without this the test
	// drops a multi-megabyte rg-unknown into the developer's real ~/.octo/bin
	// — a directory internal/tools/sandbox.go puts on the child PATH.
	home := t.TempDir()
	t.Setenv("HOME", home)        // os.UserHomeDir on unix
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on windows
	t.Setenv("PATH", "/nonexistent")

	p, err := Path()
	if err != nil {
		t.Fatalf("Path() error: %v", err)
	}
	if !filepath.IsAbs(p) {
		t.Errorf("Path() = %q, expected absolute path", p)
	}

	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat(%q): %v", p, err)
	}
	// Windows synthesises the mode from file attributes and never sets 0111
	// for regular files, so the bit only carries meaning elsewhere.
	if runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
		t.Errorf("%q is not executable", p)
	}

	// A second call must reuse the extracted copy instead of rewriting it;
	// rewriting is what lands on a live .exe on Windows. Backdating the file
	// first means a rewrite is visible regardless of filesystem timestamp
	// granularity. This only separates the two implementations on Windows —
	// on unix the executable bit already cached correctly — and it needs
	// -tags=embedrg, so TestExtracted is what CI relies on.
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(p, past, past); err != nil {
		t.Fatalf("chtimes(%q): %v", p, err)
	}
	p2, err := Path()
	if err != nil {
		t.Fatalf("second Path() error: %v", err)
	}
	if p2 != p {
		t.Errorf("second Path() = %q, want %q", p2, p)
	}
	after, err := os.Stat(p2)
	if err != nil {
		t.Fatalf("stat(%q): %v", p2, err)
	}
	if !after.ModTime().Equal(past) {
		t.Errorf("second Path() re-extracted the binary (mtime %v, want %v)",
			after.ModTime(), past)
	}

	out, err := exec.Command(p, "--version").Output()
	if err != nil {
		t.Fatalf("%q --version failed: %v", p, err)
	}
	if len(out) == 0 {
		t.Errorf("%q --version produced no output", p)
	}
}

func TestRgBinName(t *testing.T) {
	name := rgBinName()
	if runtime.GOOS == "windows" {
		if !hasSuffix(name, ".exe") {
			t.Errorf("rgBinName() = %q, expected .exe suffix on Windows", name)
		}
	} else {
		if hasSuffix(name, ".exe") {
			t.Errorf("rgBinName() = %q, unexpected .exe suffix on %s", name, runtime.GOOS)
		}
	}
	if name == "" {
		t.Error("rgBinName() returned empty string")
	}
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func TestExtracted(t *testing.T) {
	orig := embeddedRG
	defer func() { embeddedRG = orig }()
	embeddedRG = []byte("0123456789")

	dir := t.TempDir()

	if extracted(filepath.Join(dir, "absent")) {
		t.Error("extracted() = true for a missing file")
	}

	short := filepath.Join(dir, "short")
	if err := os.WriteFile(short, embeddedRG[:5], 0755); err != nil {
		t.Fatal(err)
	}
	if extracted(short) {
		t.Error("extracted() = true for a truncated copy")
	}

	full := filepath.Join(dir, "full")
	if err := os.WriteFile(full, embeddedRG, 0755); err != nil {
		t.Fatal(err)
	}
	if !extracted(full) {
		t.Error("extracted() = false for a complete copy")
	}

	if extracted(dir) {
		t.Error("extracted() = true for a directory")
	}

	if runtime.GOOS != "windows" {
		noexec := filepath.Join(dir, "noexec")
		if err := os.WriteFile(noexec, embeddedRG, 0644); err != nil {
			t.Fatal(err)
		}
		if extracted(noexec) {
			t.Error("extracted() = true for a non-executable copy")
		}
	}
}

// TestExtract_RenameFallback covers the branch where the rename loses a race
// with another process: the failure may be swallowed only when a complete
// binary is actually in place.
func TestExtract_RenameFallback(t *testing.T) {
	origEmbedded := embeddedRG
	defer func() { embeddedRG = origEmbedded }()
	embeddedRG = []byte("0123456789")

	origRename := renameFile
	defer func() { renameFile = origRename }()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	dir, err := octoBinDir()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, rgBinName())

	renameFile = func(string, string) error {
		return errors.New("Access is denied.")
	}
	if _, err := extract(); err == nil {
		t.Error("extract() error = nil when the rename failed with no binary in place")
	}

	// Now the loser of the race finds the complete copy the winner just put
	// there, which is exactly what it was about to write itself.
	renameFile = func(_, newpath string) error {
		if err := os.WriteFile(newpath, embeddedRG, 0755); err != nil {
			t.Fatal(err)
		}
		return errors.New("Access is denied.")
	}
	got, err := extract()
	if err != nil {
		t.Fatalf("extract() error = %v, want nil (complete binary in place)", err)
	}
	if got != bin {
		t.Errorf("extract() = %q, want %q", got, bin)
	}
}
