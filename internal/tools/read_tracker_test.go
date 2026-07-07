package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadTracker_NewFileWritableWithoutRead(t *testing.T) {
	rt := NewReadTracker()
	// A path that doesn't exist on disk needs no prior read.
	missing := filepath.Join(t.TempDir(), "brand-new.txt")
	if err := rt.CheckWritable(missing); err != nil {
		t.Errorf("new file should be writable without a read: %v", err)
	}
}

func TestReadTracker_ExistingUnreadFileRefused(t *testing.T) {
	rt := NewReadTracker()
	dir := t.TempDir()
	p := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(p, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := rt.CheckWritable(p)
	if err == nil || !strings.Contains(err.Error(), "not been read") {
		t.Errorf("existing unread file should be refused, got %v", err)
	}
}

func TestReadTracker_ReadThenWriteAllowed(t *testing.T) {
	rt := NewReadTracker()
	dir := t.TempDir()
	p := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(p, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	rt.RecordRead(p)
	if err := rt.CheckWritable(p); err != nil {
		t.Errorf("file read this session should be writable: %v", err)
	}
}

func TestReadTracker_ModifiedSinceReadRefused(t *testing.T) {
	rt := NewReadTracker()
	dir := t.TempDir()
	p := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(p, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	rt.RecordRead(p)

	// Bump mtime to the future to simulate an out-of-band edit.
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	err := rt.CheckWritable(p)
	if err == nil || !strings.Contains(err.Error(), "modified since") {
		t.Errorf("out-of-band modified file should be refused, got %v", err)
	}
}

// ─── Registry integration ──────────────────────────────────────────────────

func TestRegistry_ReadBeforeWrite_BlocksUnreadEdit(t *testing.T) {
	reg := NewDefaultRegistry()
	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// edit_file without a prior read → refused by the tracker, before the
	// tool itself runs.
	_, err := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "package x", "new_string": "package y",
	})
	if err == nil || !strings.Contains(err.Error(), "not been read") {
		t.Errorf("edit of unread file should be blocked, got %v", err)
	}
}

func TestRegistry_ReadBeforeWrite_AllowsAfterRead(t *testing.T) {
	reg := NewDefaultRegistry()
	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := reg.Execute(context.Background(), "read_file", map[string]any{"path": p}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if _, err := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "package x", "new_string": "package y",
	}); err != nil {
		t.Errorf("edit after read should succeed: %v", err)
	}
}

func TestRegistry_WriteThenEdit_NoRedundantRead(t *testing.T) {
	// Writing a NEW file then editing it should work without an explicit
	// read in between — the write stamps the tracker.
	reg := NewDefaultRegistry()
	dir := t.TempDir()
	p := filepath.Join(dir, "fresh.txt")

	if _, err := reg.Execute(context.Background(), "write_file", map[string]any{
		"path": p, "content": "hello\nworld\n",
	}); err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if _, err := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "hello", "new_string": "goodbye",
	}); err != nil {
		t.Errorf("edit right after write should succeed: %v", err)
	}
}

func TestSessionReadTracker_PersistsAcrossSimulatedTurns(t *testing.T) {
	sid := "sess-read-tracker-test"
	t.Cleanup(func() { CloseSessionReadTracker(sid) })

	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Turn 1: a fresh registry built from the session's tracker (mirrors
	// prepareToolTurn building a new DefaultRegistry every turn) reads the file.
	turn1 := NewDefaultRegistryWithTracker(SessionReadTracker(sid))
	if _, err := turn1.Execute(context.Background(), "read_file", map[string]any{"path": p}); err != nil {
		t.Fatalf("read_file: %v", err)
	}

	// Turn 2: a DIFFERENT DefaultRegistry value, but backed by the same
	// session tracker — the earlier read must still count.
	turn2 := NewDefaultRegistryWithTracker(SessionReadTracker(sid))
	if _, err := turn2.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "package x", "new_string": "package y",
	}); err != nil {
		t.Errorf("edit in a later turn of the same session should see the earlier turn's read: %v", err)
	}
}

func TestSessionReadTracker_IsolatedAcrossSessions(t *testing.T) {
	sidA, sidB := "sess-a", "sess-b"
	t.Cleanup(func() { CloseSessionReadTracker(sidA); CloseSessionReadTracker(sidB) })

	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	regA := NewDefaultRegistryWithTracker(SessionReadTracker(sidA))
	if _, err := regA.Execute(context.Background(), "read_file", map[string]any{"path": p}); err != nil {
		t.Fatalf("read_file: %v", err)
	}

	regB := NewDefaultRegistryWithTracker(SessionReadTracker(sidB))
	_, err := regB.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "package x", "new_string": "package y",
	})
	if err == nil || !strings.Contains(err.Error(), "not been read") {
		t.Errorf("session B should not inherit session A's read, got %v", err)
	}
}

func TestCloseSessionReadTracker_DropsState(t *testing.T) {
	sid := "sess-close-test"
	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	SessionReadTracker(sid).RecordRead(p)
	CloseSessionReadTracker(sid)
	t.Cleanup(func() { CloseSessionReadTracker(sid) })

	// A fresh tracker under the same id after close must not remember the read.
	if err := SessionReadTracker(sid).CheckWritable(p); err == nil {
		t.Errorf("tracker state should not survive CloseSessionReadTracker")
	}
}

func TestRegistry_ZeroValue_NoEnforcement(t *testing.T) {
	// DefaultRegistry{} (nil tracker) must behave as before — no
	// read-before-write gating.
	reg := DefaultRegistry{}
	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// edit without read — should NOT be blocked by the tracker (the edit
	// itself succeeds because old_string matches).
	if _, err := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "package x", "new_string": "package y",
	}); err != nil {
		t.Errorf("zero-value registry should not enforce read-before-write: %v", err)
	}
}
