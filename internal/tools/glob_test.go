package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGlob_FlatStarPattern(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.go"), "")
	writeTestFile(t, filepath.Join(dir, "b.go"), "")
	writeTestFile(t, filepath.Join(dir, "c.txt"), "")

	out, err := GlobTool{}.Execute(context.Background(), "glob", map[string]any{
		"pattern": "*.go",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.Text, "a.go") || !strings.Contains(out.Text, "b.go") {
		t.Errorf("expected a.go and b.go in output:\n%s", out.Text)
	}
	if strings.Contains(out.Text, "c.txt") {
		t.Errorf(".txt should not match *.go:\n%s", out.Text)
	}
}

func TestGlob_DoubleStarRecursive(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "top.go"), "")
	writeTestFile(t, filepath.Join(dir, "sub", "mid.go"), "")
	writeTestFile(t, filepath.Join(dir, "sub", "deeper", "leaf.go"), "")
	writeTestFile(t, filepath.Join(dir, "sub", "deeper", "other.txt"), "")

	out, err := GlobTool{}.Execute(context.Background(), "glob", map[string]any{
		"pattern": "**/*.go",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"top.go", "mid.go", "leaf.go"} {
		if !strings.Contains(out.Text, want) {
			t.Errorf("expected %s in output:\n%s", want, out.Text)
		}
	}
	if strings.Contains(out.Text, "other.txt") {
		t.Errorf("other.txt should not match")
	}
}

func TestGlob_SortedByMtimeDescending(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	older := filepath.Join(dir, "older.txt")
	newer := filepath.Join(dir, "newer.txt")
	writeTestFile(t, older, "")
	// Sleep just enough to give different mtimes on coarse-grained filesystems.
	time.Sleep(20 * time.Millisecond)
	writeTestFile(t, newer, "")

	out, err := GlobTool{}.Execute(context.Background(), "glob", map[string]any{
		"pattern": "*.txt",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// newer.txt should appear before older.txt in the output.
	idxNew := strings.Index(out.Text, "newer.txt")
	idxOld := strings.Index(out.Text, "older.txt")
	if idxNew < 0 || idxOld < 0 || idxNew > idxOld {
		t.Errorf("expected newer.txt before older.txt:\n%s", out.Text)
	}
}

func TestGlob_SkipsGitAndNodeModules(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "src", "real.go"), "")
	writeTestFile(t, filepath.Join(dir, ".git", "objects", "ignored.go"), "")
	writeTestFile(t, filepath.Join(dir, "node_modules", "pkg", "ignored.go"), "")

	out, err := GlobTool{}.Execute(context.Background(), "glob", map[string]any{
		"pattern": "**/*.go",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.Text, "real.go") {
		t.Errorf("real.go missing:\n%s", out.Text)
	}
	if strings.Contains(out.Text, ".git/") || strings.Contains(out.Text, "node_modules/") {
		t.Errorf("noise directories should be skipped:\n%s", out.Text)
	}
}

func TestGlob_RespectsGitignore(t *testing.T) {
	requireRg(t)
	// glob enumerates via ripgrep, so a .gitignore'd file is excluded even
	// though it isn't under one of the hardcoded noise dirs. ripgrep only
	// applies .gitignore at a detected repo root, so the test dir gets a .git
	// marker — the normal case, since glob runs inside real repos.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	writeTestFile(t, filepath.Join(dir, ".gitignore"), "ignored.go\n")
	writeTestFile(t, filepath.Join(dir, "kept.go"), "")
	writeTestFile(t, filepath.Join(dir, "ignored.go"), "")

	out, err := GlobTool{}.Execute(context.Background(), "glob", map[string]any{
		"pattern": "**/*.go",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.Text, "kept.go") {
		t.Errorf("kept.go missing:\n%s", out.Text)
	}
	if strings.Contains(out.Text, "ignored.go") {
		t.Errorf("gitignored file should be excluded:\n%s", out.Text)
	}
}

func TestGlob_NoMatches(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	out, err := GlobTool{}.Execute(context.Background(), "glob", map[string]any{
		"pattern": "*.nonsense",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.Text, "no matches") {
		t.Errorf("expected 'no matches' message: %q", out.Text)
	}
}

func TestGlob_RequiresPattern(t *testing.T) {
	_, err := GlobTool{}.Execute(context.Background(), "glob", map[string]any{})
	if err == nil {
		t.Fatal("missing pattern should error")
	}
}

func TestGlob_NonExistentRoot(t *testing.T) {
	requireRg(t)
	_, err := GlobTool{}.Execute(context.Background(), "glob", map[string]any{
		"pattern": "*.go",
		"path":    filepath.Join(t.TempDir(), "does-not-exist"),
	})
	if err == nil {
		t.Fatal("expected error for non-existent root")
	}
	if !strings.Contains(err.Error(), "stat root") {
		t.Errorf("error should mention stat root, got: %v", err)
	}
}

func TestGlob_FileRoot(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	single := filepath.Join(dir, "single.go")
	writeTestFile(t, single, "")

	out, err := GlobTool{}.Execute(context.Background(), "glob", map[string]any{
		"pattern": "*.go",
		"path":    single,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.Text, "single.go") {
		t.Errorf("expected single.go in output:\n%s", out.Text)
	}
}

func TestLiteralPathPrefix(t *testing.T) {
	cases := []struct {
		pattern string
		want    string
	}{
		{"internal/tools/**/*.go", "internal/tools"},
		{"cmd/octo/config.go", "cmd/octo/config.go"}, // fully literal, no wildcard at all
		{"src/**/*.ts", "src"},
		{"**/*.go", ""},
		{"*.go", ""},
		{"src/*/foo.go", "src"},
		{"../sibling/*.go", ""},        // ".." must never anchor outside the search root
		{"foo/../bar/*.go", "foo"},     // stops at ".." rather than treating it as literal
		{"/internal/tools/*.go", "internal/tools"}, // leading "/" doesn't shift the prefix
		{"./internal/tools/*.go", "internal/tools"},
	}
	for _, c := range cases {
		if got := literalPathPrefix(c.pattern); got != c.want {
			t.Errorf("literalPathPrefix(%q) = %q, want %q", c.pattern, got, c.want)
		}
	}
}

// TestGlob_PrunesToLiteralPrefix verifies a pattern anchored under a literal
// directory prefix never walks into an unrelated, unreadable sibling
// directory. Before the pruning fix, glob always walked the whole root
// (ripgrep has no reason to skip a dir it wasn't told to avoid), so the
// unreadable sibling would trip the "partial results" warning even though
// the pattern couldn't possibly match anything under it.
func TestGlob_PrunesToLiteralPrefix(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "readable", "ok.go"), "")
	writeTestFile(t, filepath.Join(dir, "unreadable", "secret.go"), "")

	unreadable := filepath.Join(dir, "unreadable")
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatalf("chmod unreadable: %v", err)
	}
	defer os.Chmod(unreadable, 0o755)

	if f, err := os.Open(unreadable); err == nil {
		f.Close()
		t.Skip("chmod 000 did not prevent directory listing; skipping permission test")
	}

	out, err := GlobTool{}.Execute(context.Background(), "glob", map[string]any{
		"pattern": "readable/*.go",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.Text, "ok.go") {
		t.Errorf("expected ok.go in output:\n%s", out.Text)
	}
	// The pattern is anchored to "readable/", so the walk should never touch
	// "unreadable/" — no warning should surface.
	if strings.Contains(out.Text, "[warning:") {
		t.Errorf("pruned walk should not have visited the unreadable sibling dir:\n%s", out.Text)
	}
}

// TestGlob_DotDotPatternNeverEscapesRoot is a regression test: an earlier
// version of the pruning logic treated ".." as an ordinary literal path
// segment, so a pattern like "../sibling/*.go" would get pruned to
// filepath.Join(root, "../sibling") — a directory outside root — and
// ripgrep would then walk and return files from there. Before pruning
// existed at all, this pattern could never match anything (no real path
// component is literally ".."), so the correct, safe behavior is "no
// matches", not "leak the parent/sibling directory's contents".
func TestGlob_DotDotPatternNeverEscapesRoot(t *testing.T) {
	requireRg(t)
	base := t.TempDir()
	root := filepath.Join(base, "root")
	sibling := filepath.Join(base, "sibling")
	writeTestFile(t, filepath.Join(root, "ok.go"), "")
	writeTestFile(t, filepath.Join(sibling, "secret.go"), "")

	out, err := GlobTool{}.Execute(context.Background(), "glob", map[string]any{
		"pattern": "../sibling/*.go",
		"path":    root,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out.Text, "secret.go") {
		t.Errorf("pattern with \"..\" must never match outside the search root:\n%s", out.Text)
	}
	if !strings.Contains(out.Text, "no matches") {
		t.Errorf("expected 'no matches', got:\n%s", out.Text)
	}
}

// TestGlob_CaseMismatchedPrefixDoesNotMatch is a regression test: os.Stat
// resolves case-insensitively on the filesystems most developers actually
// run this on (macOS APFS, Windows NTFS default), so an earlier version of
// the pruning logic trusted a case-mismatched literal prefix (e.g.
// "Readable/*.go" against a real "readable/") and pruned to it — silently
// turning a case-sensitive glob into a case-insensitive one, and reporting
// matches under a path whose casing doesn't exist on disk. On a
// case-sensitive filesystem (Linux) this bug can't reproduce since the
// os.Stat itself would fail — this test only exercises the code path, not
// the specific filesystem-dependent symptom.
func TestGlob_CaseMismatchedPrefixDoesNotMatch(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "readable", "ok.go"), "")

	out, err := GlobTool{}.Execute(context.Background(), "glob", map[string]any{
		"pattern": "Readable/*.go",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out.Text, "ok.go") {
		t.Errorf("case-mismatched pattern must not match (glob is case-sensitive):\n%s", out.Text)
	}
	if !strings.Contains(out.Text, "no matches") {
		t.Errorf("expected 'no matches', got:\n%s", out.Text)
	}
}

// TestGlob_LiteralPrefixMissingSkipsWalk verifies that when the pattern's
// literal prefix doesn't exist on disk, glob returns "no matches" without
// invoking ripgrep at all. It deliberately does not call requireRg — if this
// regresses to calling rg anyway, the test still passes as long as rg is
// present, so the meaningful signal is that this test never needs it.
func TestGlob_LiteralPrefixMissingSkipsWalk(t *testing.T) {
	dir := t.TempDir()

	out, err := GlobTool{}.Execute(context.Background(), "glob", map[string]any{
		"pattern": "does-not-exist/*.go",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.Text, "no matches") {
		t.Errorf("expected 'no matches' message: %q", out.Text)
	}
}

// TestGlob_FullyLiteralPatternMatchesSingleFile exercises the fast path
// where the entire pattern is a literal path (no wildcard characters at
// all): literalPathPrefix returns the whole pattern, which resolves
// straight to a single file instead of a directory to walk.
func TestGlob_FullyLiteralPatternMatchesSingleFile(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "pkg", "single.go"), "")
	writeTestFile(t, filepath.Join(dir, "pkg", "other.go"), "")

	out, err := GlobTool{}.Execute(context.Background(), "glob", map[string]any{
		"pattern": "pkg/single.go",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.Text, "single.go") {
		t.Errorf("expected single.go in output:\n%s", out.Text)
	}
	if strings.Contains(out.Text, "other.go") {
		t.Errorf("other.go should not match a fully literal pattern:\n%s", out.Text)
	}
}

func TestGlob_UnreadableSubdirReturnsPartialResults(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "readable", "ok.go"), "")
	writeTestFile(t, filepath.Join(dir, "unreadable", "secret.go"), "")

	unreadable := filepath.Join(dir, "unreadable")
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatalf("chmod unreadable: %v", err)
	}
	defer os.Chmod(unreadable, 0o755) // ensure cleanup can remove the tree

	// Permission restrictions may not be enforced (e.g. root on Unix, some Windows
	// configurations). If we can still list the directory, the test premise fails.
	if f, err := os.Open(unreadable); err == nil {
		f.Close()
		t.Skip("chmod 000 did not prevent directory listing; skipping permission test")
	}

	out, err := GlobTool{}.Execute(context.Background(), "glob", map[string]any{
		"pattern": "**/*.go",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.Text, "ok.go") {
		t.Errorf("expected ok.go from readable subdir:\n%s", out.Text)
	}
	if strings.Contains(out.Text, "secret.go") {
		t.Errorf("secret.go should not be readable:\n%s", out.Text)
	}
	if !strings.Contains(out.Text, "[warning:") {
		t.Errorf("expected a warning about the unreadable subdir:\n%s", out.Text)
	}
}
