package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTruncateForInjection_Flag(t *testing.T) {
	small, trunc := truncateForInjection("a\nb\nc")
	if trunc {
		t.Errorf("small input should not be truncated, got %q", small)
	}
	var big strings.Builder
	for i := 0; i < maxInjectLines+50; i++ {
		big.WriteString("line\n")
	}
	out, trunc := truncateForInjection(big.String())
	if !trunc {
		t.Error("over-line-budget input should be flagged truncated")
	}
	if got := strings.Count(out, "\n") + 1; got > maxInjectLines {
		t.Errorf("truncated output should be <= %d lines, got %d", maxInjectLines, got)
	}
}

func TestRenderInjection_TruncationWarning(t *testing.T) {
	dir := t.TempDir()
	var big strings.Builder
	for i := 0; i < maxInjectLines+20; i++ {
		big.WriteString("- entry\n")
	}
	if err := os.WriteFile(filepath.Join(dir, IndexFile), []byte(big.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got := RenderInjection(dir)
	if !strings.Contains(got, "exceeds the injection budget") {
		t.Errorf("over-budget index should inject a truncation warning; got:\n%s", got[len(got)-300:])
	}
}

func TestLint(t *testing.T) {
	// Clean file → no warnings.
	clean := t.TempDir()
	os.WriteFile(filepath.Join(clean, IndexFile), []byte("# notes\n- a pointer\n\n## 触发提醒\n- (触发: deploy) push to staging first\n"), 0o644)
	if w := Lint(clean); len(w) != 0 {
		t.Errorf("clean memory should lint clean, got %v", w)
	}

	// Triggered rule with no clause → never-recalled warning.
	bad := t.TempDir()
	os.WriteFile(filepath.Join(bad, IndexFile), []byte("## 触发提醒\n- always rebase before merging\n"), 0o644)
	w := Lint(bad)
	if len(w) == 0 || !strings.Contains(strings.Join(w, "\n"), "never be recalled") {
		t.Errorf("triggered rule with no clause should warn, got %v", w)
	}

	// Over-budget → budget warning.
	over := t.TempDir()
	var big strings.Builder
	for i := 0; i < maxInjectLines+5; i++ {
		big.WriteString("- x\n")
	}
	os.WriteFile(filepath.Join(over, IndexFile), []byte(big.String()), 0o644)
	if w := Lint(over); len(w) == 0 || !strings.Contains(strings.Join(w, "\n"), "injection budget") {
		t.Errorf("over-budget index should warn, got %v", w)
	}
}
