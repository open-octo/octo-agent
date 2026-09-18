package profiles

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/datahome"
)

func testHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(datahome.ProfileEnv, "")
	return home
}

func mkRoot(t *testing.T, home, dirName string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(home, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestList_DescribesRootsWithSizeCurrentAndRunning(t *testing.T) {
	home := testHome(t)
	t.Setenv(datahome.ProfileEnv, "work")
	mkRoot(t, home, ".octo", map[string]string{"config.yml": "abc"})
	// Our own pid is alive by definition, so this root reads as running.
	mkRoot(t, home, ".octo-lab", map[string]string{"serve.pid": itoa(os.Getpid()) + "\n"})
	// A dead pid is not a running backend.
	mkRoot(t, home, ".octo-work", map[string]string{"serve.pid": "999999999\n", "a": "12345"})

	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("List() = %+v, want 3 entries", got)
	}
	byName := map[string]Info{}
	for _, in := range got {
		byName[in.Name] = in
	}
	if d := byName[""]; d.SizeBytes != 3 || d.Current || d.Running || d.Path != filepath.Join(home, ".octo") {
		t.Errorf("default = %+v", d)
	}
	if l := byName["lab"]; !l.Running || l.Pid != os.Getpid() || l.Current {
		t.Errorf("lab = %+v, want running under our pid", l)
	}
	if w := byName["work"]; !w.Current || w.Running || w.Pid != 0 {
		t.Errorf("work = %+v, want current and not running", w)
	}
}

func TestCreate_MakesRootAndRejectsDuplicates(t *testing.T) {
	home := testHome(t)
	info, err := Create("scratch")
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != filepath.Join(home, ".octo-scratch") {
		t.Errorf("Path = %q", info.Path)
	}
	if st, err := os.Stat(info.Path); err != nil || !st.IsDir() {
		t.Fatalf("root not created: %v", err)
	}
	if _, err := Create("scratch"); !errors.Is(err, ErrExists) {
		t.Errorf("second Create = %v, want ErrExists", err)
	}
	for _, bad := range []string{"", "-lead", "has space", "a/b", "..", "x::y"} {
		if _, err := Create(bad); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Create(%q) = %v, want ErrInvalidName", bad, err)
		}
	}
}

func TestRemove_Guards(t *testing.T) {
	home := testHome(t)
	t.Setenv(datahome.ProfileEnv, "work")
	mkRoot(t, home, ".octo", nil)
	mkRoot(t, home, ".octo-work", nil)
	mkRoot(t, home, ".octo-lab", map[string]string{"serve.pid": itoa(os.Getpid()) + "\n"})

	if err := Remove(""); !errors.Is(err, ErrDefault) {
		t.Errorf("Remove(default) = %v, want ErrDefault", err)
	}
	if err := Remove("work"); !errors.Is(err, ErrCurrent) {
		t.Errorf("Remove(current) = %v, want ErrCurrent", err)
	}
	if err := Remove("lab"); !errors.Is(err, ErrRunning) {
		t.Errorf("Remove(running) = %v, want ErrRunning", err)
	}
	if err := Remove("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Remove(missing) = %v, want ErrNotFound", err)
	}
	if err := Remove("bad name"); !errors.Is(err, ErrInvalidName) {
		t.Errorf("Remove(invalid) = %v, want ErrInvalidName", err)
	}
	for _, name := range []string{".octo", ".octo-work", ".octo-lab"} {
		if _, err := os.Stat(filepath.Join(home, name)); err != nil {
			t.Errorf("%s was touched by a refused Remove: %v", name, err)
		}
	}
}

func TestRemove_DeletesAnIdleNamedProfile(t *testing.T) {
	home := testHome(t)
	mkRoot(t, home, ".octo-old", map[string]string{"serve.pid": "999999999\n"})
	mkRoot(t, home, filepath.Join(".octo-old", "sessions"), map[string]string{"x.jsonl": ""})
	if err := Remove("old"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".octo-old")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("root still present after Remove: %v", err)
	}
}

func itoa(n int) string {
	return string(appendInt(nil, n))
}

func appendInt(b []byte, n int) []byte {
	if n >= 10 {
		b = appendInt(b, n/10)
	}
	return append(b, byte('0'+n%10))
}
