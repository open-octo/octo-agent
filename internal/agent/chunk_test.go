package agent

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestArchiveChunk(t *testing.T) {
	a := New(&summarizeFake{}, "m")
	a.ArchiveDir = t.TempDir()
	msgs := []Message{
		NewUserMessage("do the thing"),
		{Role: RoleAssistant, Blocks: []ContentBlock{NewToolUseBlock("c1", "read_file", map[string]any{"path": "x.go"})}},
		{Role: RoleUser, Blocks: []ContentBlock{NewToolResultBlock("c1", "file contents here", false)}},
	}
	ref := a.archiveChunk(msgs)
	if ref == "" {
		t.Fatal("expected a chunk path")
	}
	data, err := os.ReadFile(ref)
	if err != nil {
		t.Fatalf("read chunk: %v", err)
	}
	got := string(data)
	for _, want := range []string{"do the thing", "read_file", "file contents here"} {
		if !strings.Contains(got, want) {
			t.Errorf("chunk missing %q; got:\n%s", want, got)
		}
	}
	// A second archive must not overwrite the first.
	if ref2 := a.archiveChunk(msgs); ref2 == ref {
		t.Errorf("second chunk should get a fresh path; both = %s", ref)
	}
}

func TestArchiveChunk_DisabledWhenNoDir(t *testing.T) {
	a := New(&summarizeFake{}, "m")
	if ref := a.archiveChunk([]Message{NewUserMessage("x")}); ref != "" {
		t.Errorf("no ArchiveDir → no archival, got %q", ref)
	}
}

// maybeCompact archives the folded originals and points the summary at them.
func TestMaybeCompact_ArchivesFoldedTurns(t *testing.T) {
	f := &summarizeFake{summary: "GOAL: build X."}
	a := New(f, "m")
	a.CompactThreshold = 100
	a.ArchiveDir = t.TempDir()

	longMsg := strings.Repeat("x ", 500)
	for i := 0; i < 6; i++ {
		a.History.Append(NewUserMessage(longMsg))
		a.History.Append(NewAssistantMessage(longMsg))
	}
	if err := a.maybeCompact(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	summary := a.History.Snapshot()[0].Content
	if !strings.Contains(summary, "archived at") || !strings.Contains(summary, "read tool") {
		t.Errorf("summary should reference the archive; got %q", summary)
	}
	entries, _ := os.ReadDir(a.ArchiveDir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 chunk file, got %d", len(entries))
	}
}
