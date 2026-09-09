package rgembed

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
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

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "/nonexistent")
	defer os.Setenv("PATH", origPath)

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
	// on Windows rewriting means renaming over an .exe that a concurrent
	// grep/glob may still be running, which fails with "Access is denied".
	before, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat(%q): %v", p, err)
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
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("second Path() re-extracted the binary (mtime %v -> %v)",
			before.ModTime(), after.ModTime())
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
