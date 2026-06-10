package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestParseSource(t *testing.T) {
	cases := []struct {
		in   string
		want Source
		err  bool
	}{
		{in: "anthropics/skills/skills/docx", want: Source{Owner: "anthropics", Repo: "skills", Subpath: "skills/docx"}},
		{in: "anthropics/skills", want: Source{Owner: "anthropics", Repo: "skills"}},
		{in: "https://github.com/anthropics/skills/tree/main/skills/pdf", want: Source{Owner: "anthropics", Repo: "skills", Ref: "main", Subpath: "skills/pdf"}},
		{in: "https://github.com/anthropics/skills", want: Source{Owner: "anthropics", Repo: "skills"}},
		{in: "https://github.com/anthropics/skills/blob/main/skills/pdf", err: true},
		{in: "https://gitlab.com/x/y/tree/main/z", err: true},
		{in: "justonename", err: true},
		{in: "", err: true},
	}
	for _, c := range cases {
		got, err := ParseSource(c.in)
		if c.err {
			if err == nil {
				t.Errorf("ParseSource(%q): want error, got %+v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSource(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseSource(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// makeTarball builds a gzipped tarball with GitHub's owner-repo-sha/ top-level
// prefix on every entry.
func makeTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{
			Name: "anthropics-skills-0abc123/" + name,
			Mode: 0o644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// serveTarball points tarballURL at an httptest server returning body for the
// test's duration.
func serveTarball(t *testing.T, body []byte) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	orig := tarballURL
	tarballURL = func(Source) string { return srv.URL }
	t.Cleanup(func() {
		tarballURL = orig
		srv.Close()
	})
}

const validSkillMd = "---\nname: docx\ndescription: Work with Word documents.\n---\nbody\n"

func TestInstall_ExtractsSubpathOnly(t *testing.T) {
	serveTarball(t, makeTarball(t, map[string]string{
		"skills/docx/SKILL.md":            validSkillMd,
		"skills/docx/scripts/__init__.py": "",
		"skills/docx/scripts/pack.py":     "print('hi')",
		"skills/pdf/SKILL.md":             "---\nname: pdf\ndescription: other\n---\n",
		"README.md":                       "root readme",
	}))

	root := t.TempDir()
	name, desc, err := Install(Source{Owner: "a", Repo: "s", Subpath: "skills/docx"}, root, false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if name != "docx" || desc != "Work with Word documents." {
		t.Errorf("name=%q desc=%q", name, desc)
	}
	for _, f := range []string{"SKILL.md", "scripts/__init__.py", "scripts/pack.py"} {
		if _, err := os.Stat(filepath.Join(root, "docx", f)); err != nil {
			t.Errorf("expected %s installed: %v", f, err)
		}
	}
	// Files outside the subpath must not leak in.
	if _, err := os.Stat(filepath.Join(root, "docx", "README.md")); !os.IsNotExist(err) {
		t.Error("README.md from repo root leaked into the skill")
	}
	if entries, _ := os.ReadDir(root); len(entries) != 1 {
		t.Errorf("expected exactly the installed skill in root, got %d entries", len(entries))
	}
}

func TestInstall_RefusesExistingWithoutForce(t *testing.T) {
	serveTarball(t, makeTarball(t, map[string]string{"skills/docx/SKILL.md": validSkillMd}))

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Install(Source{Owner: "a", Repo: "s", Subpath: "skills/docx"}, root, false); err == nil {
		t.Fatal("want error installing over an existing skill without force")
	}
	if _, _, err := Install(Source{Owner: "a", Repo: "s", Subpath: "skills/docx"}, root, true); err != nil {
		t.Fatalf("force install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docx", "SKILL.md")); err != nil {
		t.Errorf("force install should have replaced the skill: %v", err)
	}
}

func TestInstall_SubpathMissing(t *testing.T) {
	serveTarball(t, makeTarball(t, map[string]string{"README.md": "x"}))
	if _, _, err := Install(Source{Owner: "a", Repo: "s", Subpath: "skills/nope"}, t.TempDir(), false); err == nil {
		t.Fatal("want error for a subpath absent from the tarball")
	}
}

func TestInstall_RejectsInvalidSkillMd(t *testing.T) {
	serveTarball(t, makeTarball(t, map[string]string{"skills/docx/SKILL.md": "no frontmatter here"}))
	if _, _, err := Install(Source{Owner: "a", Repo: "s", Subpath: "skills/docx"}, t.TempDir(), false); err == nil {
		t.Fatal("want error for SKILL.md without frontmatter")
	}
}

func TestInstall_RejectsPathTraversal(t *testing.T) {
	serveTarball(t, makeTarball(t, map[string]string{
		"skills/docx/SKILL.md":   validSkillMd,
		"skills/docx/../../evil": "boom",
	}))
	root := t.TempDir()
	if _, _, err := Install(Source{Owner: "a", Repo: "s", Subpath: "skills/docx"}, root, false); err == nil {
		t.Fatal("want error for a tar entry escaping the skill directory")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil")); !os.IsNotExist(err) {
		t.Error("traversal entry was written outside the dest root")
	}
}

func TestInstall_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	orig := tarballURL
	tarballURL = func(Source) string { return srv.URL }
	t.Cleanup(func() {
		tarballURL = orig
		srv.Close()
	})
	if _, _, err := Install(Source{Owner: "a", Repo: "nope"}, t.TempDir(), false); err == nil {
		t.Fatal("want error on HTTP 404")
	}
}

func TestInstall_RepoRootSkill(t *testing.T) {
	serveTarball(t, makeTarball(t, map[string]string{
		"SKILL.md": "---\nname: solo\ndescription: a whole-repo skill\n---\n",
		"ref.md":   "extra",
	}))
	root := t.TempDir()
	name, _, err := Install(Source{Owner: "a", Repo: "solo-skill"}, root, false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if name != "solo" {
		t.Errorf("name = %q, want solo (from frontmatter)", name)
	}
	if _, err := os.Stat(filepath.Join(root, "solo", "ref.md")); err != nil {
		t.Errorf("ref.md missing: %v", err)
	}
}
