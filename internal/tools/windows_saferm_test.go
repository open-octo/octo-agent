package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/trash"
)

// TestWindowsSafeRm_EndToEnd drives the real PowerShell Remove-Item wrapper
// against a freshly built octo binary: a delete must both remove the file AND
// leave it recoverable in the trash, for a relative and an absolute path. It is
// the Windows counterpart of TestSafeRm_AbsolutePathBackedUp.
//
// Windows-only: the wrapper shadows the Windows PowerShell Remove-Item cmdlet
// and shells out to `octo __trash-backup`. It builds octo with `go build`, so
// it's a slower integration test guarded to the platform it protects.
func TestWindowsSafeRm_EndToEnd(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows Remove-Item wrapper only")
	}
	ps := resolvePowerShell()
	if _, err := exec.LookPath(ps); err != nil {
		t.Skipf("no PowerShell (%s) available", ps)
	}

	// Build the octo binary the wrapper calls back into.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	exe := filepath.Join(t.TempDir(), "octo.exe")
	build := exec.Command("go", "build", "-o", exe, "github.com/open-octo/octo-agent/cmd/octo")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build octo: %v\n%s", err, out)
	}

	workdir := t.TempDir()
	rel := filepath.Join(workdir, "rel.txt")
	abs := filepath.Join(t.TempDir(), "abs.txt") // absolute, outside workdir
	for _, p := range []string{rel, abs} {
		if err := os.WriteFile(p, []byte("payload"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Mirror shellCommand's wiring: %s = octo exe (single-quote escaped), then
	// the user command. -Force ghost.txt must be ignored (non-existent path and
	// a -flag), proving only real filesystem paths are staged.
	command := "Remove-Item rel.txt; Remove-Item '" + abs + "'; Remove-Item -Force ghost.txt"
	wrapped := fmt.Sprintf(windowsSafeRmWrapper, strings.ReplaceAll(exe, "'", "''"), command)
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", wrapped)
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(), "OCTO_TRASH_PROJECT="+workdir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper run: %v\n%s", err, out)
	}

	// Both targets deleted by the real cmdlet.
	for _, p := range []string{rel, abs} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should have been deleted", p)
		}
	}

	// Both recoverable, stamped rm/delete; the ghost was never staged. Match by
	// basename: PowerShell's Resolve-Path canonicalizes to the long path form
	// while Go's t.TempDir may hand back an 8.3 short path, so the full
	// "original" strings won't be equal on Windows.
	entries, err := trash.List()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]trash.Entry{}
	for _, e := range entries {
		seen[filepath.Base(e.Original)] = e
	}
	for _, base := range []string{"rel.txt", "abs.txt"} {
		e, ok := seen[base]
		if !ok {
			t.Errorf("%s not backed up (have %v)", base, entryBasenames(entries))
			continue
		}
		if e.DeletedBy != "rm" || e.Kind != "delete" {
			t.Errorf("%s provenance = %q/%q, want rm/delete", base, e.DeletedBy, e.Kind)
		}
	}
	if _, ok := seen["ghost.txt"]; ok {
		t.Error("a non-existent target must not be staged")
	}
}

func entryBasenames(entries []trash.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = filepath.Base(e.Original)
	}
	return out
}
