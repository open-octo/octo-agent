package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/open-octo/octo-agent/internal/datahome"
)

// ensureBundledUv copies the uv binary shipped with the app into the
// profile-scoped Octo data root's bin directory on first launch, so skills that
// need Python work out of the box even for a standalone download that never went
// through the installer. uv is agent-level infrastructure the toolchain looks
// for on PATH or in that directory (internal/tools/toolchain.go); this just
// seeds that fallback. Best-effort and idempotent: a no-op once uv is present,
// or if no bundled copy is found.
func ensureBundledUv() {
	target, err := bundledUvTarget()
	if err != nil {
		return
	}
	if _, err := os.Stat(target); err == nil {
		return // already seeded (by us or the installer)
	}
	src := bundledBinaryPath(filepath.Base(target))
	if src == "" {
		return // this build doesn't ship uv
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return
	}
	_ = copyExecutable(src, target)
}

// bundledUvTarget returns the profile-scoped destination for the bundled uv binary.
func bundledUvTarget() (string, error) {
	name := "uv"
	if runtime.GOOS == "windows" {
		name = "uv.exe"
	}
	return datahome.Path("bin", name)
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
	// PID in the temp name so two instances launched before the single-instance
	// lock is taken don't write the same .tmp and rename a half-written binary.
	tmp := fmt.Sprintf("%s.tmp.%d", dst, os.Getpid())
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
