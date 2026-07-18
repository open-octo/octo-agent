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

// A file the session itself reformats through the terminal tool (gofmt -w,
// sed -i, a redirect) must still be editable afterwards — the terminal write
// is the session's own change, not an out-of-band edit, so it should refresh
// the tracker rather than trip the "modified since read" guard.
//
// The sed/semicolon, sed/pipe, and bash-c cases here are regression tests for
// bugs where the tokenizer glued a trailing shell metacharacter to the filename
// (`file.go;`) or the command was wrapped in `bash -c "..."`, both of which
// made the write target unparseable and left the tracker unrefreshed.
func TestRegistry_TerminalWriteThenEdit_Allowed(t *testing.T) {
	cases := []struct {
		name    string
		command func(p string) string
	}{
		{"redirect", func(p string) string { return "printf 'package x\\nconst c = 3\\n' > " + p }},
		{"redirect-fused", func(p string) string { return "printf 'package x\\nconst c = 3\\n' >" + p }},
		{"sed-inplace", func(p string) string { return "sed -i '' 's/const a = 1/const a = 2/' " + p }},
		{"sed-inplace-semicolon", func(p string) string { return "sed -i 's/const a = 1/const a = 2/' " + p + "; echo done" }},
		{"sed-inplace-pipe", func(p string) string { return "sed -i 's/const a = 1/const a = 2/' " + p + " | cat" }},
		{"bash-c-sed", func(p string) string { return "bash -c \"sed -i 's/const a = 1/const a = 2/' " + p + "\"" }},
		{"sh-c-sed", func(p string) string { return "sh -c \"sed -i 's/const a = 1/const a = 2/' " + p + "\"" }},
		{"gofmt-w-file", func(p string) string { return "gofmt -w " + p }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewDefaultRegistry()
			dir := t.TempDir()
			p := filepath.Join(dir, "code.go")
			if err := os.WriteFile(p, []byte("package x\nconst a = 1\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := reg.Execute(context.Background(), "read_file", map[string]any{"path": p}); err != nil {
				t.Fatalf("read_file: %v", err)
			}
			// Push the file's mtime forward so the terminal write is unambiguously
			// "newer than the read" regardless of filesystem mtime resolution.
			future := time.Now().Add(2 * time.Hour)
			if err := os.Chtimes(p, future, future); err != nil {
				t.Fatal(err)
			}
			if _, err := reg.Execute(context.Background(), "terminal", map[string]any{"command": tc.command(p)}); err != nil {
				t.Fatalf("terminal: %v", err)
			}
			if _, err := reg.Execute(context.Background(), "edit_file", map[string]any{
				"path": p, "old_string": "package x", "new_string": "package y",
			}); err != nil {
				t.Errorf("edit after the session's own terminal write should succeed: %v", err)
			}
		})
	}
}

// A directory/whole-tree write target (`gofmt -w .`) is NOT followed to the
// files beneath it: only files the command names exactly are refreshed. A
// tracked sibling the command didn't name keeps its stale stamp, so a genuine
// out-of-band edit to it stays blocked — the write detection can't be used to
// launder an external edit through a broad formatter invocation.
func TestRegistry_TerminalWriteDir_DoesNotRefreshSiblings(t *testing.T) {
	reg := NewDefaultRegistry()
	dir := t.TempDir()
	p := filepath.Join(dir, "notes.md") // not a file gofmt would rewrite
	if err := os.WriteFile(p, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Execute(context.Background(), "read_file", map[string]any{"path": p}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	// Out-of-band editor bumps the sibling, then the agent runs a whole-dir
	// formatter that names the directory, not this file.
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Execute(context.Background(), "terminal", map[string]any{"command": "gofmt -w " + dir}); err != nil {
		t.Fatalf("terminal: %v", err)
	}
	_, err := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "hello", "new_string": "goodbye",
	})
	if err == nil || !strings.Contains(err.Error(), "modified since") {
		t.Errorf("external edit to an unnamed sibling should stay blocked, got %v", err)
	}
}

// A file the session writes through the terminal but never read must still be
// unwritable — RefreshTarget only re-stamps already-tracked paths, so a write
// command can't substitute for a read. Uses a `printf >` redirect: printf is
// not a read-style command, so recordTerminalReads doesn't tag it either.
func TestRegistry_TerminalWriteUnreadFile_StillBlocked(t *testing.T) {
	reg := NewDefaultRegistry()
	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Never read; only overwritten via a redirect.
	if _, err := reg.Execute(context.Background(), "terminal", map[string]any{
		"command": "printf 'package x\\nconst c = 3\\n' > " + p,
	}); err != nil {
		t.Fatalf("terminal: %v", err)
	}
	_, err := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "package x", "new_string": "package y",
	})
	if err == nil || !strings.Contains(err.Error(), "not been read") {
		t.Errorf("editing a never-read file should be blocked, got %v", err)
	}
}

// The guard must still fire for a genuine out-of-band edit: a terminal command
// that neither reads nor writes the file must not refresh its mtime, so write
// detection can't be tricked into adopting an external editor's change.
func TestRegistry_ExternalEditAfterUnrelatedCommand_StillBlocked(t *testing.T) {
	reg := NewDefaultRegistry()
	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Execute(context.Background(), "read_file", map[string]any{"path": p}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	// Simulate an out-of-band editor bumping the file, then a terminal command
	// that doesn't mention it at all.
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Execute(context.Background(), "terminal", map[string]any{"command": "echo done"}); err != nil {
		t.Fatalf("terminal: %v", err)
	}
	_, err := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "package x", "new_string": "package y",
	})
	if err == nil || !strings.Contains(err.Error(), "modified since") {
		t.Errorf("external edit should still be blocked after an unrelated command, got %v", err)
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
