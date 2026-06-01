package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDir_PerRepoUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	d, err := Dir("/some/path/to/myrepo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(d, filepath.Join(home, ".octo", "memory")) {
		t.Errorf("dir %q not under ~/.octo/memory", d)
	}
	if !strings.Contains(filepath.Base(d), "myrepo") {
		t.Errorf("dir slug %q should carry the repo basename", filepath.Base(d))
	}

	// Same basename, different path → different dir (hash disambiguates).
	d2, _ := Dir("/other/place/myrepo")
	if d == d2 {
		t.Errorf("two repos named myrepo collided: %q", d)
	}
}

func TestLoadIndex_TruncatesToBudget(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < maxInjectLines+50; i++ {
		b.WriteString("line\n")
	}
	if err := os.WriteFile(filepath.Join(dir, IndexFile), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadIndex(dir)
	if n := strings.Count(got, "\n") + 1; n > maxInjectLines {
		t.Errorf("LoadIndex returned %d lines, want <= %d", n, maxInjectLines)
	}

	// Absent file → empty.
	if LoadIndex(t.TempDir()) != "" {
		t.Error("LoadIndex of an empty dir should be \"\"")
	}
}

func TestRenderInjection(t *testing.T) {
	dir := t.TempDir()

	// Empty memory still emits the instruction + the empty marker so the model
	// knows where to start.
	out := RenderInjection(dir)
	if !strings.Contains(out, dir) {
		t.Errorf("injection should name the memory dir; got:\n%s", out)
	}
	if !strings.Contains(out, "is empty") {
		t.Errorf("empty memory should be marked; got:\n%s", out)
	}

	// With content, the index follows the instruction.
	if err := os.WriteFile(filepath.Join(dir, IndexFile), []byte("- prefers Go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = RenderInjection(dir)
	if !strings.Contains(out, "prefers Go") {
		t.Errorf("injection should include MEMORY.md content; got:\n%s", out)
	}
	if !strings.Contains(out, "## "+IndexFile) {
		t.Errorf("injection should head the content with the index name; got:\n%s", out)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"My Repo!":      "my-repo",
		"  spaced  out": "spaced-out",
		"already-kebab": "already-kebab",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
