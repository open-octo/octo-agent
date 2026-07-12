package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runSafeRm runs `command` through the POSIX safe-rm wrapper with the given
// working directory and trash dir, exactly as shellCommand wires it.
func runSafeRm(t *testing.T, workdir, trashDir, command string) {
	t.Helper()
	wrapped := fmt.Sprintf(safeRmWrapper, command)
	cmd := exec.Command("sh", "-c", wrapped)
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(), "OCTO_TRASH_DIR="+trashDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper run failed: %v\n%s", err, out)
	}
}

// trashedOriginals returns the "original" field of every .meta.json in trashDir.
func trashedOriginals(t *testing.T, trashDir string) []string {
	t.Helper()
	var got []string
	entries, _ := os.ReadDir(trashDir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".meta.json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(trashDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, string(b))
	}
	return got
}

// TestSafeRm_AbsolutePathBackedUp is the regression guard for the bug where the
// wrapper copied from "$PWD/$arg" unconditionally: an absolute-path `rm`
// argument copied from a non-existent path, staged nothing, and the real rm
// still deleted it. Both a relative and an absolute target must be deleted AND
// recoverable from the trash.
func TestSafeRm_AbsolutePathBackedUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper only")
	}
	workdir := t.TempDir()
	trashDir := t.TempDir()
	elsewhere := t.TempDir()

	rel := filepath.Join(workdir, "rel.txt")
	abs := filepath.Join(elsewhere, "abs.txt")
	if err := os.WriteFile(rel, []byte("rel"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("abs"), 0o644); err != nil {
		t.Fatal(err)
	}

	runSafeRm(t, workdir, trashDir, "rm rel.txt; rm "+abs)

	// Both originals are gone (real rm ran).
	if _, err := os.Stat(rel); !os.IsNotExist(err) {
		t.Errorf("relative file should be deleted")
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Errorf("absolute file should be deleted")
	}

	// Both are recoverable: two backups, and the absolute path recorded as-is.
	metas := trashedOriginals(t, trashDir)
	if len(metas) != 2 {
		t.Fatalf("expected 2 trashed entries, got %d: %v", len(metas), metas)
	}
	var sawRel, sawAbs bool
	for _, m := range metas {
		if strings.Contains(m, rel) {
			sawRel = true
		}
		if strings.Contains(m, `"original": "`+abs+`"`) || strings.Contains(m, `"original":"`+abs+`"`) {
			sawAbs = true
		}
	}
	if !sawRel {
		t.Errorf("relative delete not backed up; metas=%v", metas)
	}
	if !sawAbs {
		t.Errorf("absolute delete not backed up (the bug); metas=%v", metas)
	}
	for _, m := range metas {
		if !strings.Contains(m, `"deleted_by":"rm"`) {
			t.Errorf("rm-staged meta should record deleted_by=rm; got %s", m)
		}
	}
}

// TestSafeRm_NoTrashDirIsNoOp: with $OCTO_TRASH_DIR unset the wrapper must not
// block the delete.
func TestSafeRm_NoTrashDirIsNoOp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper only")
	}
	workdir := t.TempDir()
	f := filepath.Join(workdir, "f.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	wrapped := fmt.Sprintf(safeRmWrapper, "rm f.txt")
	cmd := exec.Command("sh", "-c", wrapped)
	cmd.Dir = workdir
	// No OCTO_TRASH_DIR in env.
	cmd.Env = append(os.Environ(), "OCTO_TRASH_DIR=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Error("delete should still happen with no trash dir configured")
	}
}
