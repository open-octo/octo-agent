package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadFile_RefusesBinaryContent: the extension check misses binaries
// with an unknown or missing extension — the content sniff must catch the
// NUL bytes every real binary format carries in its header.
func TestReadFile_RefusesBinaryContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.out")
	if err := os.WriteFile(path, []byte("\x7fELF\x02\x01\x01\x00\x00\x00some machine code"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadFileTool{}.Execute(context.Background(), "read_file", map[string]any{"path": path})
	if err == nil {
		t.Fatal("expected refusal for binary content")
	}
	if !strings.Contains(err.Error(), "looks binary") || !strings.Contains(err.Error(), "terminal") {
		t.Errorf("refusal should name the binary sniff and point at the terminal tool, got: %v", err)
	}
}

// TestReadFile_RefusesUTF16WithConversionHint: UTF-16 text is NUL-laced by
// encoding, not by nature — it gets a conversion hint, not the binary lump.
func TestReadFile_RefusesUTF16WithConversionHint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.log")
	// "hi\n" as UTF-16LE with BOM — what Windows PowerShell 5 redirection writes.
	if err := os.WriteFile(path, []byte{0xff, 0xfe, 'h', 0x00, 'i', 0x00, '\n', 0x00}, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadFileTool{}.Execute(context.Background(), "read_file", map[string]any{"path": path})
	if err == nil {
		t.Fatal("expected refusal for UTF-16 content")
	}
	if !strings.Contains(err.Error(), "UTF-16") || !strings.Contains(err.Error(), "Convert") {
		t.Errorf("refusal should name UTF-16 and suggest conversion, got: %v", err)
	}
}

// TestReadFile_RefusesUTF16BENoNUL: a big-endian BOM must be recognized even
// when the first 512 bytes contain no NUL at all (pure non-ASCII text, where
// UTF-16BE code units have no zero byte) — the BOM check must not be gated
// behind the NUL check.
func TestReadFile_RefusesUTF16BENoNUL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cjk16.txt")
	// UTF-16BE BOM + "中文" (U+4E2D U+6587) — no zero bytes anywhere.
	if err := os.WriteFile(path, []byte{0xfe, 0xff, 0x4e, 0x2d, 0x65, 0x87}, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadFileTool{}.Execute(context.Background(), "read_file", map[string]any{"path": path})
	if err == nil {
		t.Fatal("expected refusal for UTF-16BE content")
	}
	if !strings.Contains(err.Error(), "UTF-16") {
		t.Errorf("refusal should name UTF-16, got: %v", err)
	}
}

// TestReadFile_SniffAllowsUTF8AndRewinds: multi-byte UTF-8 (and a NUL past
// the 512-byte sniff window) must not trip the sniff, and the rewind must
// leave the scanner reading from line 1.
func TestReadFile_SniffAllowsUTF8AndRewinds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cjk.txt")
	content := "第一行 中文\n" + strings.Repeat("padding line\n", 100) + "tail\n"
	writeTestFile(t, path, content)

	out, err := ReadFileTool{}.Execute(context.Background(), "read_file", map[string]any{"path": path})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.Text, "     1\t第一行 中文") {
		t.Errorf("first line missing after sniff rewind:\n%s", out.Text[:min(len(out.Text), 200)])
	}
	if !strings.Contains(out.Text, "tail") {
		t.Errorf("last line missing after sniff rewind")
	}
}
