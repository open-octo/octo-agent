package main

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// ensureBundledUv copies the uv binary shipped with the app into ~/.octo/bin on
// first launch, so skills that need Python work out of the box even for a
// standalone download that never went through the installer. uv is agent-level
// infrastructure the toolchain looks for on PATH or in ~/.octo/bin
// (internal/tools/toolchain.go); this just seeds that fallback. Best-effort and
// idempotent: a no-op once uv is present, or if no bundled copy is found.
func ensureBundledUv() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	name := "uv"
	if runtime.GOOS == "windows" {
		name = "uv.exe"
	}
	target := filepath.Join(home, ".octo", "bin", name)
	if _, err := os.Stat(target); err == nil {
		return // already seeded (by us or the installer)
	}
	src := bundledBinaryPath(name)
	if src == "" {
		return // this build doesn't ship uv
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return
	}
	_ = copyExecutable(src, target)
}

// bundledBinaryPath locates a binary shipped alongside the app (uv, or the octo
// CLI), whichever way it was packaged: beside the binary (Windows dir, Linux),
// in the macOS .app's Resources, or under an AppImage's mount ($APPDIR).
func bundledBinaryPath(name string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(dir, name),
		filepath.Join(dir, "..", "Resources", name), // macOS: Contents/MacOS → Contents/Resources
	}
	if appdir := os.Getenv("APPDIR"); appdir != "" {
		candidates = append(candidates, filepath.Join(appdir, "usr", "bin", name))
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// copyExecutable copies src to dst (0755) via a temp file + rename so a crash
// mid-copy can't leave a truncated, executable binary behind. Returns an error
// so callers that persist "seeded version" state only record success.
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
