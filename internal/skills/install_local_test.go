package skills

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// makeZip writes a zip with the given entries to a temp file and returns its path.
func makeZip(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, content := range files {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestInstallZip_RootLayout(t *testing.T) {
	zp := makeZip(t, "docx.zip", map[string]string{
		"SKILL.md":            validSkillMd,
		"scripts/__init__.py": "",
		"scripts/run.py":      "x",
	})
	root := t.TempDir()
	name, desc, err := InstallZip(zp, root, false)
	if err != nil {
		t.Fatalf("InstallZip: %v", err)
	}
	if name != "docx" || desc == "" {
		t.Errorf("name=%q desc=%q", name, desc)
	}
	for _, f := range []string{"SKILL.md", "scripts/__init__.py", "scripts/run.py"} {
		if _, err := os.Stat(filepath.Join(root, "docx", f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
}

func TestInstallZip_SingleFolderLayout(t *testing.T) {
	zp := makeZip(t, "bundle.zip", map[string]string{
		"docx/SKILL.md":     validSkillMd,
		"docx/reference.md": "ref",
		"docx/scripts/x.py": "x",
	})
	root := t.TempDir()
	name, _, err := InstallZip(zp, root, false)
	if err != nil {
		t.Fatalf("InstallZip: %v", err)
	}
	if name != "docx" {
		t.Errorf("name = %q", name)
	}
	if _, err := os.Stat(filepath.Join(root, "docx", "reference.md")); err != nil {
		t.Errorf("folder prefix not stripped: %v", err)
	}
}

func TestInstallZip_NoSkillMd(t *testing.T) {
	zp := makeZip(t, "junk.zip", map[string]string{"readme.txt": "hi"})
	if _, _, err := InstallZip(zp, t.TempDir(), false); err == nil {
		t.Fatal("want error for archive without SKILL.md")
	}
}

func TestInstallZip_PathTraversal(t *testing.T) {
	zp := makeZip(t, "evil.zip", map[string]string{
		"SKILL.md":  validSkillMd,
		"../escape": "boom",
	})
	root := t.TempDir()
	if _, _, err := InstallZip(zp, root, false); err == nil {
		t.Fatal("want error for traversal entry")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape")); !os.IsNotExist(err) {
		t.Error("traversal entry written outside root")
	}
}

func TestInstallZip_ExistsAndForce(t *testing.T) {
	zp := makeZip(t, "docx.zip", map[string]string{"SKILL.md": validSkillMd})
	root := t.TempDir()
	if _, _, err := InstallZip(zp, root, false); err != nil {
		t.Fatal(err)
	}
	_, _, err := InstallZip(zp, root, false)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}
	if _, _, err := InstallZip(zp, root, true); err != nil {
		t.Fatalf("force: %v", err)
	}
}

func TestInstallDir(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(src, "SKILL.md"), "---\nname: mydir\ndescription: from a dir\n---\nbody")
	mustWrite(t, filepath.Join(src, "scripts", "go.py"), "x")

	root := t.TempDir()
	name, desc, err := InstallDir(src, root, false)
	if err != nil {
		t.Fatalf("InstallDir: %v", err)
	}
	if name != "mydir" || desc != "from a dir" {
		t.Errorf("name=%q desc=%q", name, desc)
	}
	if _, err := os.Stat(filepath.Join(root, "mydir", "scripts", "go.py")); err != nil {
		t.Errorf("nested file missing: %v", err)
	}
	// Source directory is copied, not moved.
	if _, err := os.Stat(filepath.Join(src, "SKILL.md")); err != nil {
		t.Errorf("source was consumed: %v", err)
	}
}

func TestInstallDir_NotADir(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file.txt")
	mustWrite(t, f, "x")
	if _, _, err := InstallDir(f, t.TempDir(), false); err == nil {
		t.Fatal("want error for non-directory source")
	}
}

func TestInstall_GitHubExistsReturnsErrExists(t *testing.T) {
	serveTarball(t, makeTarball(t, map[string]string{"skills/docx/SKILL.md": validSkillMd}))
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docx"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := Install(Source{Owner: "a", Repo: "s", Subpath: "skills/docx"}, root, false)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}
}
