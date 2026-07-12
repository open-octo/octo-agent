package logfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write is a helper that fails the test on a short write or error.
func write(t *testing.T, w *Rotating, s string) {
	t.Helper()
	n, err := w.Write([]byte(s))
	if err != nil {
		t.Fatalf("write %q: %v", s, err)
	}
	if n != len(s) {
		t.Fatalf("write %q: short write %d/%d", s, n, len(s))
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestRotatesBeforeCrossingCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serve.log")
	w, err := Open(path, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	write(t, w, "aaaaa\n") // 6 bytes, file was empty → no rotate, size 6
	write(t, w, "bbbbb\n") // 6 bytes, 6+6 > 10 → rotate first, then write into fresh file

	if got := read(t, path); got != "bbbbb\n" {
		t.Errorf("live file = %q, want %q", got, "bbbbb\n")
	}
	if got := read(t, path+".1"); got != "aaaaa\n" {
		t.Errorf("rotated .1 = %q, want %q", got, "aaaaa\n")
	}
	if _, err := os.Stat(path + ".2"); !os.IsNotExist(err) {
		t.Errorf(".2 should not exist yet")
	}
}

func TestNeverExceedsCapByMoreThanOneRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serve.log")
	w, err := Open(path, 100, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	for i := 0; i < 200; i++ {
		write(t, w, fmt.Sprintf("line-%03d\n", i)) // 9 bytes each
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Live file holds at most one record beyond the cap.
	if fi.Size() > 100+9 {
		t.Errorf("live file size %d exceeds cap+record", fi.Size())
	}
}

func TestBackupsChainDropsOldest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serve.log")
	w, err := Open(path, 10, 2) // keep serve.log, .1, .2 → 3 generations total
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Each 6-byte record after the first forces a rotation.
	for _, r := range []string{"AAAAA\n", "BBBBB\n", "CCCCC\n", "DDDDD\n"} {
		write(t, w, r)
	}
	// Newest in live file, older ones shifted; the very oldest (AAAAA) dropped.
	if got := read(t, path); got != "DDDDD\n" {
		t.Errorf("live = %q, want DDDDD", got)
	}
	if got := read(t, path+".1"); got != "CCCCC\n" {
		t.Errorf(".1 = %q, want CCCCC", got)
	}
	if got := read(t, path+".2"); got != "BBBBB\n" {
		t.Errorf(".2 = %q, want BBBBB", got)
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Errorf(".3 should not exist (backups=2)")
	}
}

// TestRotateOverwritesExistingBackup exercises the Windows-safe path: a second
// rotation must replace an already-present .1, which requires removing the
// destination before the rename (os.Rename onto an existing file fails on
// Windows).
func TestRotateOverwritesExistingBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serve.log")
	w, err := Open(path, 10, 1) // only one backup slot, so .1 gets overwritten
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	write(t, w, "first\n")
	write(t, w, "secnd\n") // rotate: first → .1
	write(t, w, "third\n") // rotate again: .1 (first) dropped, secnd → .1

	if got := read(t, path); got != "third\n" {
		t.Errorf("live = %q, want third", got)
	}
	if got := read(t, path+".1"); got != "secnd\n" {
		t.Errorf(".1 = %q, want secnd", got)
	}
}

func TestOversizedSingleRecordWrittenWhole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serve.log")
	w, err := Open(path, 8, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	big := strings.Repeat("x", 50) + "\n" // far larger than the 8-byte cap
	write(t, w, big)
	if got := read(t, path); got != big {
		t.Errorf("oversized record truncated/rotated: got %d bytes, want %d", len(got), len(big))
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Errorf("nothing should have rotated on the first oversized write")
	}
}

func TestOpenRotatesPreexistingOversizedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serve.log")
	// Simulate a log grown unbounded before rotation was introduced.
	if err := os.WriteFile(path, []byte(strings.Repeat("old", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := Open(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// The oversized content moved to .1; the live file starts fresh.
	if fi, err := os.Stat(path); err != nil || fi.Size() != 0 {
		t.Errorf("live file not fresh after open-time rotation: size=%v err=%v", fiSize(fi), err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("expected pre-existing content rotated to .1: %v", err)
	}
}

func TestRotateIfLarger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serve.log")

	// Missing file: no-op, no error.
	if err := RotateIfLarger(path, 100, 2); err != nil {
		t.Fatalf("missing file should be a no-op: %v", err)
	}

	// Below the bound: left in place.
	if err := os.WriteFile(path, []byte("small"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RotateIfLarger(path, 100, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Errorf("small file should not rotate")
	}

	// At/over the bound: rotated to .1, original path gone.
	if err := os.WriteFile(path, []byte(strings.Repeat("y", 150)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RotateIfLarger(path, 100, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("oversized file should have been renamed away")
	}
	if got := read(t, path+".1"); len(got) != 150 {
		t.Errorf(".1 = %d bytes, want 150", len(got))
	}
}

func fiSize(fi os.FileInfo) int64 {
	if fi == nil {
		return -1
	}
	return fi.Size()
}
