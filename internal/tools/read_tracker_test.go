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
		// The cd-prefixed shapes below mirror how the agent works inside a
		// worktree. On Windows CI the Unix writers don't exist; the terminal
		// folds the failure into its text, so these cases verify the parser's
		// attribution there rather than the shell's behaviour.
		{"cd-and-gofmt-relative", func(p string) string {
			return "cd " + filepath.Dir(p) + " && gofmt -w " + filepath.Base(p) + " && echo BUILD_OK"
		}},
		{"cd-and-sed-relative", func(p string) string {
			return "cd " + filepath.Dir(p) + " && sed -i '' 's/const a = 1/const a = 2/' " + filepath.Base(p) + " && echo done"
		}},
		{"cd-semicolon-gofmt-relative", func(p string) string {
			return "cd " + filepath.Dir(p) + "; gofmt -w " + filepath.Base(p) + " 2>&1 | tail -5"
		}},
		{"cd-and-redirect-relative", func(p string) string {
			return "cd " + filepath.Dir(p) + " && printf 'package x\\nconst c = 3\\n' > " + filepath.Base(p)
		}},
		{"bash-c-cd-and-gofmt-relative", func(p string) string {
			return "bash -c \"cd " + filepath.Dir(p) + " && gofmt -w " + filepath.Base(p) + "\""
		}},
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

// ─── grep counts as a read ────────────────────────────────────────────────

// A grep over a directory surfaces the matching lines of every hit file, so
// those files count as "read": a following edit_file must not be refused.
func TestRegistry_GrepDirThenEdit_Allowed(t *testing.T) {
	requireRg(t)
	reg := NewDefaultRegistry()
	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\nconst a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := reg.Execute(context.Background(), "grep", map[string]any{
		"pattern": "const a", "path": dir,
	}); err != nil {
		t.Fatalf("grep: %v", err)
	}
	if _, err := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "const a = 1", "new_string": "const a = 2",
	}); err != nil {
		t.Errorf("edit after grep should succeed: %v", err)
	}
}

// files_with_matches mode shows only paths — still enough to count as seen.
func TestRegistry_GrepFilesWithMatchesThenEdit_Allowed(t *testing.T) {
	requireRg(t)
	reg := NewDefaultRegistry()
	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\nconst a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := reg.Execute(context.Background(), "grep", map[string]any{
		"pattern": "const a", "path": dir, "mode": "files_with_matches",
	}); err != nil {
		t.Fatalf("grep: %v", err)
	}
	if _, err := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "const a = 1", "new_string": "const a = 2",
	}); err != nil {
		t.Errorf("edit after files_with_matches grep should succeed: %v", err)
	}
}

// Single-file grep: rg omits the path prefix from its output, so the file
// must be picked up from the input's own `path` argument.
func TestRegistry_GrepSingleFileThenEdit_Allowed(t *testing.T) {
	requireRg(t)
	reg := NewDefaultRegistry()
	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\nconst a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := reg.Execute(context.Background(), "grep", map[string]any{
		"pattern": "const a", "path": p,
	}); err != nil {
		t.Fatalf("grep: %v", err)
	}
	if _, err := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "const a = 1", "new_string": "const a = 2",
	}); err != nil {
		t.Errorf("edit after single-file grep should succeed: %v", err)
	}
}

// Only files the grep actually surfaced count. A sibling with no matches was
// never shown to the model, so editing it stays blocked.
func TestRegistry_GrepThenEdit_UnmatchedFileStillBlocked(t *testing.T) {
	requireRg(t)
	reg := NewDefaultRegistry()
	dir := t.TempDir()
	hit := filepath.Join(dir, "hit.go")
	miss := filepath.Join(dir, "miss.go")
	if err := os.WriteFile(hit, []byte("package x\nconst a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(miss, []byte("package x\nvar b = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := reg.Execute(context.Background(), "grep", map[string]any{
		"pattern": "const a", "path": dir,
	}); err != nil {
		t.Fatalf("grep: %v", err)
	}
	_, err := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path": miss, "old_string": "var b = 2", "new_string": "var b = 3",
	})
	if err == nil || !strings.Contains(err.Error(), "not been read") {
		t.Errorf("editing a file grep did not surface should stay blocked, got %v", err)
	}
}

// A grep with zero matches returns "(no matches)" with a nil error. That
// output must NOT stamp the input path — the model saw nothing, so a
// following edit would be a blind write the read-before-write guard must
// still block. This is the regression guard for the no-match stamp bug.
func TestRegistry_GrepSingleFileNoMatch_EditStillBlocked(t *testing.T) {
	requireRg(t)
	reg := NewDefaultRegistry()
	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\nconst a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := reg.Execute(context.Background(), "grep", map[string]any{
		"pattern": "zzz_no_such_pattern", "path": p,
	}); err != nil {
		t.Fatalf("grep: %v", err)
	}
	_, err := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "const a = 1", "new_string": "const a = 2",
	})
	if err == nil || !strings.Contains(err.Error(), "not been read") {
		t.Errorf("edit after a zero-match grep should stay blocked, got %v", err)
	}
}

// Count mode outputs "path:N" lines. Those must count as a read too.
func TestRegistry_GrepCountModeThenEdit_Allowed(t *testing.T) {
	requireRg(t)
	reg := NewDefaultRegistry()
	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\nconst a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := reg.Execute(context.Background(), "grep", map[string]any{
		"pattern": "const a", "path": dir, "mode": "count",
	}); err != nil {
		t.Fatalf("grep: %v", err)
	}
	if _, err := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "const a = 1", "new_string": "const a = 2",
	}); err != nil {
		t.Errorf("edit after count-mode grep should succeed: %v", err)
	}
}

// Context mode adds "path-N-text" lines and "--" group separators. The
// separator line must not break parsing, and the hit file must still count.
func TestRegistry_GrepContextModeThenEdit_Allowed(t *testing.T) {
	requireRg(t)
	reg := NewDefaultRegistry()
	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\nconst a = 1\nvar b = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := reg.Execute(context.Background(), "grep", map[string]any{
		"pattern": "const a", "path": dir, "context_lines": 1,
	}); err != nil {
		t.Fatalf("grep: %v", err)
	}
	if _, err := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "const a = 1", "new_string": "const a = 2",
	}); err != nil {
		t.Errorf("edit after context-mode grep should succeed: %v", err)
	}
}

// Filenames with embedded separator+digit runs (e.g. "report-2024-01-01.txt")
// used to fail closed: the non-greedy regex settled on the first "-2024-"
// boundary and the candidate "report" didn't stat. The multi-boundary fix
// now tries every prefix, so the full filename is found.
func TestRegistry_GrepNumericDashFilename_Allowed(t *testing.T) {
	requireRg(t)
	reg := NewDefaultRegistry()
	dir := t.TempDir()
	p := filepath.Join(dir, "report-2024-01-01.txt")
	if err := os.WriteFile(p, []byte("line one\nconst a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := reg.Execute(context.Background(), "grep", map[string]any{
		"pattern": "const a", "path": dir,
	}); err != nil {
		t.Fatalf("grep: %v", err)
	}
	if _, err := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "const a = 1", "new_string": "const a = 2",
	}); err != nil {
		t.Errorf("edit after grep on a numeric-dash filename should succeed: %v", err)
	}
}

// Command shapes that name "code.go" without the shell ever writing the
// tracked code.go must NOT refresh its stamp: a relative target resolves
// against the directory `cd` moved to (not the session's), a `cd` inside
// `bash -c` or a `( … )` subshell never changes the outer directory, and
// heredoc body lines are data, not commands. In every case the tracked file
// was changed out-of-band, so the follow-up edit must still be refused.
func TestRegistry_WriteNotOfTrackedFile_StillBlocked(t *testing.T) {
	cases := []struct {
		name    string
		command func(other string) string
	}{
		{"cd-elsewhere-relative", func(other string) string {
			return "cd " + other + " && gofmt -w code.go"
		}},
		{"bash-c-cd-elsewhere-relative", func(other string) string {
			return "bash -c \"cd " + other + " && gofmt -w code.go\""
		}},
		{"subshell-cd-elsewhere", func(other string) string {
			return "( cd " + other + " && gofmt -w code.go )"
		}},
		{"subshell-cd-elsewhere-tight", func(other string) string {
			return "(cd " + other + " && gofmt -w code.go)"
		}},
		{"heredoc-body-mentions-file", func(other string) string {
			return "cat > " + filepath.Join(other, "fmt.sh") + " <<'EOF'\n#!/bin/sh\ngofmt -w code.go\nsed -i '' 's/a/b/' code.go\nEOF"
		}},
		// A directory change the parser can see but not follow marks the
		// directory lost, so the relative target is not attributed at all.
		{"brace-group-cd", func(other string) string {
			return "{ cd " + other + "; gofmt -w code.go; }"
		}},
		{"for-do-cd", func(other string) string {
			return "for d in " + other + "; do cd " + other + "; gofmt -w code.go; done"
		}},
		{"command-cd", func(other string) string {
			return "command cd " + other + " && gofmt -w code.go"
		}},
		{"backslash-cd", func(other string) string {
			return "\\cd " + other + " && gofmt -w code.go"
		}},
		{"eval-cd", func(other string) string {
			return "eval \"cd " + other + "\" && gofmt -w code.go"
		}},
		{"substitution-cd", func(other string) string {
			return "echo $(cd " + other + " && gofmt -w code.go && echo ok)"
		}},
		{"pushd", func(other string) string {
			return "pushd " + other + " >/dev/null && gofmt -w code.go"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewDefaultRegistry()
			dir := t.TempDir()
			other := t.TempDir()
			p := filepath.Join(dir, "code.go")
			if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(other, "code.go"), []byte("package y\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			ctx := WithWorkingDir(context.Background(), dir)
			if _, err := reg.Execute(ctx, "read_file", map[string]any{"path": p}); err != nil {
				t.Fatalf("read_file: %v", err)
			}
			future := time.Now().Add(2 * time.Hour)
			if err := os.Chtimes(p, future, future); err != nil {
				t.Fatal(err)
			}
			if _, err := reg.Execute(ctx, "terminal", map[string]any{"command": tc.command(other)}); err != nil {
				t.Fatalf("terminal: %v", err)
			}
			_, err := reg.Execute(ctx, "edit_file", map[string]any{
				"path": p, "old_string": "package x", "new_string": "package y",
			})
			if err == nil || !strings.Contains(err.Error(), "modified since") {
				t.Errorf("command never wrote the tracked file, edit must stay blocked, got %v", err)
			}
		})
	}
}

// Following `cd` does not loosen the directory rule: `cd dir && gofmt -w .`
// still names a directory, and a tracked file beneath it keeps its stale
// stamp exactly as it does without the cd prefix.
func TestRegistry_CdThenWriteDir_DoesNotRefreshSiblings(t *testing.T) {
	reg := NewDefaultRegistry()
	dir := t.TempDir()
	p := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(p, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Execute(context.Background(), "read_file", map[string]any{"path": p}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Execute(context.Background(), "terminal", map[string]any{
		"command": "cd " + dir + " && gofmt -w . && echo OK",
	}); err != nil {
		t.Fatalf("terminal: %v", err)
	}
	_, err := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "hello", "new_string": "goodbye",
	})
	if err == nil || !strings.Contains(err.Error(), "modified since") {
		t.Errorf("directory-level write behind cd must not refresh files beneath it, got %v", err)
	}
}

// `cd -` goes somewhere the parser can't know, so a relative target after it
// is not attributed to any file — the tracked file stays blocked rather than
// being refreshed against a guessed directory.
func TestRegistry_CdDashThenWrite_StillBlocked(t *testing.T) {
	reg := NewDefaultRegistry()
	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Execute(context.Background(), "read_file", map[string]any{"path": p}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Execute(context.Background(), "terminal", map[string]any{
		"command": "cd " + dir + " && cd - >/dev/null; gofmt -w code.go",
	}); err != nil {
		t.Fatalf("terminal: %v", err)
	}
	_, err := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "package x", "new_string": "package y",
	})
	if err == nil || !strings.Contains(err.Error(), "modified since") {
		t.Errorf("relative write after cd - must not be attributed, got %v", err)
	}
}

// A read-style command behind `cd … &&` with a relative path counts as a
// read of the file the shell actually opened, so the follow-up edit is not
// refused with "not been read yet".
func TestRegistry_CdThenTerminalReadThenEdit_Allowed(t *testing.T) {
	reg := NewDefaultRegistry()
	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Execute(context.Background(), "terminal", map[string]any{
		"command": "cd " + dir + " && cat code.go",
	}); err != nil {
		t.Fatalf("terminal: %v", err)
	}
	if _, err := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path": p, "old_string": "package x", "new_string": "package y",
	}); err != nil {
		t.Errorf("edit after cd && cat should succeed: %v", err)
	}
}

// Without any cd, a relative target resolves against the session's working
// directory (where the terminal actually ran), not the process CWD.
func TestRegistry_TerminalWriteRelativeToWorkingDir_Allowed(t *testing.T) {
	reg := NewDefaultRegistry()
	dir := t.TempDir()
	p := filepath.Join(dir, "code.go")
	if err := os.WriteFile(p, []byte("package x\nconst a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := WithWorkingDir(context.Background(), dir)
	if _, err := reg.Execute(ctx, "read_file", map[string]any{"path": p}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Execute(ctx, "terminal", map[string]any{"command": "gofmt -w code.go"}); err != nil {
		t.Fatalf("terminal: %v", err)
	}
	if _, err := reg.Execute(ctx, "edit_file", map[string]any{
		"path": p, "old_string": "package x", "new_string": "package y",
	}); err != nil {
		t.Errorf("edit after a working-dir-relative terminal write should succeed: %v", err)
	}
}

func TestSplitShellSegments(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"gofmt -w a.go", []string{"gofmt -w a.go"}},
		{"cd /x && gofmt -w a.go && go build ./...", []string{"cd /x ", " gofmt -w a.go ", " go build ./..."}},
		{"a; b || c | d", []string{"a", " b ", " c ", " d"}},
		{"go test ./... 2>&1 | tail -5", []string{"go test ./... 2>&1 ", " tail -5"}},
		// Separators inside quotes belong to the token, not the shell.
		{"sed -i 's/a;b/c|d/' f.go && echo \"x && y\"", []string{"sed -i 's/a;b/c|d/' f.go ", " echo \"x && y\""}},
		{"echo one\necho two", []string{"echo one", "echo two"}},
		// A heredoc body is data: the split stops at the newline after `<<`.
		{"cat > f.sh <<'EOF'\ngofmt -w a.go\nEOF\ngofmt -w b.go", []string{"cat > f.sh <<'EOF'"}},
	}
	for _, tc := range cases {
		got := splitShellSegments(tc.in)
		if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
			t.Errorf("splitShellSegments(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

func TestShellSegments_FollowsCd(t *testing.T) {
	// Real absolute paths so the assertions hold on Windows, where "/w" is
	// not absolute and would be joined onto the base.
	root := t.TempDir()
	base := filepath.Join(root, "base")
	w := filepath.Join(root, "w")
	abs := filepath.Join(root, "abs", "c.go")

	segs := shellSegments("cd "+w+" && gofmt -w a.go; cd sub && sed -i '' 's/x/y/' b.go && cd - && cat c.go", base)
	if len(segs) != 3 {
		t.Fatalf("want 3 segments, got %d: %+v", len(segs), segs)
	}
	if segs[0].dir != w || segs[0].lost {
		t.Errorf("segment 0: want dir %s, got %+v", w, segs[0])
	}
	if want := filepath.Join(w, "sub"); segs[1].dir != want || segs[1].lost {
		t.Errorf("segment 1: want dir %s, got %+v", want, segs[1])
	}
	if !segs[2].lost {
		t.Errorf("segment 2: cd - must mark the directory lost, got %+v", segs[2])
	}
	if got, ok := segs[2].resolve("c.go"); ok {
		t.Errorf("relative path after cd - must not resolve, got %q", got)
	}
	if got, ok := segs[2].resolve(abs); !ok || got != abs {
		t.Errorf("absolute path after cd - must still resolve, got %q %v", got, ok)
	}

	plain := shellSegments("gofmt -w a.go", base)
	if len(plain) != 1 || plain[0].dir != base {
		t.Errorf("no cd: want base dir kept, got %+v", plain)
	}

	// A wrapper's payload is expanded in place: its cd is followed for the
	// inner segments (blank lines included) and never leaks to the outer line.
	wrapped := shellSegments("bash -c 'cd "+w+"\n   \ngofmt -w a.go' && gofmt -w b.go", base)
	if len(wrapped) != 2 {
		t.Fatalf("wrapped: want 2 segments, got %d: %+v", len(wrapped), wrapped)
	}
	if wrapped[0].dir != w || wrapped[0].tokens[0] != "gofmt" {
		t.Errorf("wrapped inner: want gofmt in %s, got %+v", w, wrapped[0])
	}
	if wrapped[1].dir != base {
		t.Errorf("wrapped outer: inner cd must not leak, got %+v", wrapped[1])
	}

	// A bare subshell hides where its cd stops applying, so everything from
	// it on is lost; `$(…)` substitution is not a subshell.
	sub := shellSegments("( cd "+w+" && gofmt -w a.go ) && gofmt -w b.go", base)
	for i, seg := range sub {
		if !seg.lost {
			t.Errorf("subshell segment %d must be lost, got %+v", i, seg)
		}
	}
	if subst := shellSegments("gofmt -w $(ls) && gofmt -w b.go", base); len(subst) != 2 || subst[1].lost {
		t.Errorf("$(…) must not mark the line lost, got %+v", subst)
	}

	// Directory changes followCd can't reproduce all mark the rest of the
	// line lost — including PowerShell's spellings, which only run on the
	// Windows CI leg but must be recognised everywhere.
	for _, cmd := range []string{
		"Set-Location " + w + "; gofmt -w a.go",
		"sl " + w + "; gofmt -w a.go",
		"chdir " + w + " && gofmt -w a.go",
		"Push-Location " + w + " && gofmt -w a.go",
		"pushd " + w + " >/dev/null && gofmt -w a.go",
		"popd && gofmt -w a.go",
		"eval 'cd " + w + "' && gofmt -w a.go",
		"{ cd " + w + "; gofmt -w a.go; }",
		"echo $(cd " + w + " && gofmt -w a.go)",
		"cd ~nobody && gofmt -w a.go",
		"cd -- " + w + " && gofmt -w a.go",
		"cd " + w + " 2>/dev/null && gofmt -w a.go",
		"cd -P " + w + " && gofmt -w a.go",
	} {
		segs := shellSegments(cmd, base)
		var gofmtSeg *shellSegment
		for i := range segs {
			if segs[i].tokens[0] == "gofmt" {
				gofmtSeg = &segs[i]
			}
		}
		if gofmtSeg == nil {
			t.Errorf("%q: gofmt segment not found in %+v", cmd, segs)
			continue
		}
		if !gofmtSeg.lost {
			t.Errorf("%q: directory must be lost, got %+v", cmd, *gofmtSeg)
		}
		if abs, ok := gofmtSeg.resolve("a.go"); ok {
			t.Errorf("%q: relative target must not resolve, got %q", cmd, abs)
		}
	}
}
