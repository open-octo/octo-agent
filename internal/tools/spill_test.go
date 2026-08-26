package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// spillHome points ~/.octo at a temp dir so spill files don't touch the real
// home, and cleans up after the test.
func spillHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestMaybeSpillOutput_SmallPassesThrough(t *testing.T) {
	spillHome(t)
	body := "short output\nwith a few lines\n"
	if got := MaybeSpillOutput("bg_1", body); got != body {
		t.Errorf("small output should pass through unchanged, got:\n%s", got)
	}
}

func TestMaybeSpillOutput_LargeSpillsToFile(t *testing.T) {
	spillHome(t)
	// Many short lines well over the byte threshold and the line preview.
	var b strings.Builder
	total := 2000
	for i := 0; i < total; i++ {
		b.WriteString("this is a line of build output number ")
		b.WriteByte(byte('0' + i%10))
		b.WriteByte('\n')
	}
	body := b.String()
	if len(body) <= TerminalSpillBytes {
		t.Fatalf("test setup: body should exceed threshold (%d)", len(body))
	}

	out := MaybeSpillOutput("bg_42", body)

	// Preview must mention the spill, the total line count, and a path.
	if !strings.Contains(out, "output too long") {
		t.Errorf("expected spill marker, got:\n%s", out[:min(len(out), 300)])
	}
	if !strings.Contains(out, "term-bg_42-") {
		t.Errorf("expected spill file path in preview, got:\n%s", out)
	}
	// Inline preview must be far smaller than the full body.
	if len(out) >= len(body) {
		t.Errorf("preview (%d) should be smaller than body (%d)", len(out), len(body))
	}

	// The path named in the preview must exist and hold the full body.
	path := extractSpillPath(t, out)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("spill file %q unreadable: %v", path, err)
	}
	if string(data) != body {
		t.Errorf("spill file should hold the full body verbatim (%d vs %d bytes)", len(data), len(body))
	}

	// CleanSpillFiles must remove this process's spill files.
	CleanSpillFiles()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("CleanSpillFiles should have removed %q (err=%v)", path, err)
	}
}

func TestMaybeSpillOutput_FewLongLinesAreCapped(t *testing.T) {
	spillHome(t)
	// Over the byte threshold in only a handful of (very long) lines — the
	// shape a \r-rewritten progress bar arrives in. The line bounds can't touch
	// it, so the byte cap has to: this used to pass through whole, and one such
	// notice took ~250 KB of the context window.
	body := strings.Repeat("x", 300*1024) + "\n" + strings.Repeat("y", 100)

	out := MaybeSpillOutput("bg_7", body)

	if len(out) > spillPreviewMaxBytes+512 {
		t.Errorf("preview should be capped near %d bytes, got %d", spillPreviewMaxBytes, len(out))
	}
	if !strings.Contains(out, "elided to fit context") {
		t.Errorf("expected an elision marker, got:\n%s", out[:min(len(out), 300)])
	}
	// The full body is still recoverable from the file the preview names.
	data, err := os.ReadFile(extractSpillPath(t, out))
	if err != nil {
		t.Fatalf("spill file unreadable: %v", err)
	}
	if string(data) != body {
		t.Errorf("spill file should hold the full body (%d vs %d bytes)", len(data), len(body))
	}
}

func TestMaybeSpillOutput_LongLinesInsideLinePreviewAreCapped(t *testing.T) {
	spillHome(t)
	// Enough lines to trigger the head+tail split, but with enormous lines
	// inside the kept head — the case that let a spilled background notice
	// still reach history at 316 KB.
	var b strings.Builder
	for i := 0; i < spillHeadLines+spillTailLines+10; i++ {
		b.WriteString(strings.Repeat("z", 4*1024))
		b.WriteByte('\n')
	}
	body := b.String()

	out := MaybeSpillOutput("bg_8", body)

	if len(out) > spillPreviewMaxBytes+512 {
		t.Errorf("preview should stay near %d bytes, got %d", spillPreviewMaxBytes, len(out))
	}
	if !strings.Contains(out, "output too long") {
		t.Errorf("expected spill marker, got:\n%s", out[:min(len(out), 300)])
	}
}

func TestCapSpillSegment_KeepsValidUTF8(t *testing.T) {
	// Cutting mid-rune would put invalid UTF-8 on the JSON wire. CJK makes
	// every cut land inside a 3-byte rune unless the trims do their job.
	body := strings.Repeat("云识别测试", 4096)
	got := capSpillSegment(body, 1024)
	if !utf8.ValidString(got) {
		t.Errorf("capped segment must be valid UTF-8")
	}
	if len(got) > 1024 {
		t.Errorf("capped segment = %d bytes, want <= 1024", len(got))
	}
	if !strings.HasPrefix(got, "云") {
		t.Errorf("head should survive, got %q", got[:12])
	}
	if !strings.HasSuffix(got, "试") {
		t.Errorf("tail should survive, got %q", got[len(got)-12:])
	}
}

func TestCapSpillSegment_ShortInputUntouched(t *testing.T) {
	body := "exit status 1\n"
	if got := capSpillSegment(body, 1024); got != body {
		t.Errorf("segment within budget should be returned unchanged, got %q", got)
	}
}

func TestSpillCleanup_CoversWebFetchPrefix(t *testing.T) {
	// web_fetch spill files (webfetch-…) must be reclaimed by the same age-sweep
	// and shutdown-clean that handle term- files — otherwise they leak forever.
	spillHome(t)
	dir, err := spillDir()
	if err != nil {
		t.Fatalf("spillDir: %v", err)
	}

	// A web_fetch spill file for THIS process (pid suffix), plus a stale one
	// from a long-dead session that the age-sweep should reap.
	mine, err := writeWebFetchSpillFile("https://example.com/page", []byte("body"))
	if err != nil {
		t.Fatalf("writeWebFetchSpillFile: %v", err)
	}
	stale := filepath.Join(dir, "webfetch-old-host-1-999999.log")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	if err := os.Chtimes(stale, time.Now().Add(-48*time.Hour), time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	sweepOldSpillFiles(dir)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("age-sweep should have removed the stale webfetch file (err=%v)", err)
	}

	CleanSpillFiles()
	if _, err := os.Stat(mine); !os.IsNotExist(err) {
		t.Errorf("CleanSpillFiles should have removed this process's webfetch file (err=%v)", err)
	}
}

// extractSpillPath pulls the temp-file path out of the preview marker.
func extractSpillPath(t *testing.T, preview string) string {
	t.Helper()
	for _, line := range strings.Split(preview, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "term-bg_") && strings.HasSuffix(line, ".log") {
			return line
		}
	}
	t.Fatalf("no spill path found in preview:\n%s", preview)
	return ""
}
