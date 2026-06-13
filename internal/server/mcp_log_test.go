package server

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestMCPStderrWriter_LineBuffering verifies the writer splits incoming bytes
// into whole lines, drops blank ones, and tags each record source=mcp — even
// when a single logical line is delivered across multiple Write calls.
func TestMCPStderrWriter_LineBuffering(t *testing.T) {
	var out bytes.Buffer
	h := slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug})
	w := newMCPStderrWriter(slog.New(h))

	// A full line, a blank line (should be dropped), and a line split across
	// two writes with no trailing newline on the first chunk.
	_, _ = w.Write([]byte("[CodeGraph MCP] Auto-synced 1 file\n\n"))
	_, _ = w.Write([]byte("partial "))
	_, _ = w.Write([]byte("line done\n"))

	got := out.String()
	if !strings.Contains(got, "Auto-synced 1 file") {
		t.Errorf("missing first line; got %q", got)
	}
	if !strings.Contains(got, "partial line done") {
		t.Errorf("split line not reassembled; got %q", got)
	}
	if !strings.Contains(got, "source=mcp") {
		t.Errorf("missing source=mcp tag; got %q", got)
	}
	// The blank line between the two newlines must not produce a record.
	if n := strings.Count(got, "source=mcp"); n != 2 {
		t.Errorf("expected 2 logged lines, got %d: %q", n, got)
	}
}

// TestMCPStderrWriter_NoTrailingNewline confirms a chunk without a newline is
// buffered (not emitted) until the line completes.
func TestMCPStderrWriter_NoTrailingNewline(t *testing.T) {
	var out bytes.Buffer
	h := slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug})
	w := newMCPStderrWriter(slog.New(h))

	_, _ = w.Write([]byte("incomplete"))
	if out.Len() != 0 {
		t.Errorf("buffered partial line should not log yet; got %q", out.String())
	}
	_, _ = w.Write([]byte("\n"))
	if !strings.Contains(out.String(), "incomplete") {
		t.Errorf("completed line not logged; got %q", out.String())
	}
}
