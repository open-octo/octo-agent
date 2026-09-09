// Package rgembed provides a bundled ripgrep (rg) binary for the current
// platform. At build time the Makefile downloads the matching release asset
// from BurntSushi/ripgrep and places it in binaries/rg; go:embed bakes it
// into the octo binary when the "embedrg" build tag is set. At runtime, if
// the system does not have rg on PATH, the embedded binary is extracted to
// ~/.octo/bin/rg and used instead.
package rgembed

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// version is the ripgrep release version embedded at build time.
// Overridden via -ldflags in the Makefile.
var version = "unknown"

// embeddedRG is declared in rgembed_embed.go (build tag embedrg) or
// rgembed_noembed.go (!embedrg). The split keeps CI from failing when
// binaries/rg is absent.

// Path returns the absolute path to a working rg binary.
// If rg is on PATH it returns "rg" directly; otherwise it extracts the
// embedded binary to ~/.octo/bin/rg-<version> and returns that path.
func Path() (string, error) {
	if _, err := exec.LookPath("rg"); err == nil {
		return "rg", nil
	}
	return extract()
}

// extract writes the embedded rg binary to the user cache dir and returns
// its path. The destination includes the version so upgrading octo (which
// bumps the embedded version) automatically extracts a fresh binary.
func extract() (string, error) {
	if len(embeddedRG) == 0 {
		return "", fmt.Errorf(
			"rgembed: ripgrep (`rg`) is not on PATH and no binary was embedded " +
				"at build time (build with -tags=embedrg to bundle one). " +
				"Install rg from https://github.com/BurntSushi/ripgrep",
		)
	}

	dir, err := octoBinDir()
	if err != nil {
		return "", fmt.Errorf("rgembed: cache dir: %w", err)
	}
	bin := filepath.Join(dir, rgBinName())

	// Already extracted and valid — nothing to do.
	if extracted(bin) {
		return bin, nil
	}

	// Write to a temp file in the same dir, then rename atomically.
	tmp, err := os.CreateTemp(dir, "rg-*")
	if err != nil {
		return "", fmt.Errorf("rgembed: create temp: %w", err)
	}
	if _, err := tmp.Write(embeddedRG); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("rgembed: write binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("rgembed: close temp: %w", err)
	}

	// Make executable (owner + group + other).
	if err := os.Chmod(tmp.Name(), 0755); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("rgembed: chmod: %w", err)
	}

	if err := os.Rename(tmp.Name(), bin); err != nil {
		os.Remove(tmp.Name())
		// Windows refuses to replace an .exe that any process is currently
		// running, so a concurrent grep/glob whose rg is still alive makes
		// this rename fail with "Access is denied". A complete copy already
		// sitting at bin is precisely what we were trying to write, so treat
		// the race as success rather than failing the tool call.
		if extracted(bin) {
			return bin, nil
		}
		return "", fmt.Errorf("rgembed: rename: %w", err)
	}

	return bin, nil
}

// extracted reports whether bin already holds a complete copy of the embedded
// binary. Size is the portable signal: on Windows os.Stat synthesises the mode
// from file attributes and only ever sets 0111 for directories, so an
// executable-bit check there never matches a cached copy and every call
// re-extracts. Unix keeps the bit check as an extra guard.
func extracted(bin string) bool {
	info, err := os.Stat(bin)
	if err != nil || info.IsDir() || info.Size() != int64(len(embeddedRG)) {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode()&0111 != 0
}

// octoBinDir returns ~/.octo/bin, creating it if necessary.
func octoBinDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".octo", "bin")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// rgBinName returns the platform-specific binary name.
// On Windows it includes .exe; the version is baked in so upgrades
// automatically extract a fresh binary.
func rgBinName() string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("rg-%s.exe", version)
	}
	return fmt.Sprintf("rg-%s", version)
}
