package main

import (
	"os"
	"path/filepath"
	"testing"
)

// unsetenvForTest clears an env var for the test and restores its prior
// state (set or unset) afterwards — t.Setenv can't express "was unset".
func unsetenvForTest(t *testing.T, key string) {
	t.Helper()
	if orig, ok := os.LookupEnv(key); ok {
		t.Cleanup(func() { os.Setenv(key, orig) })
	}
	os.Unsetenv(key)
}

func TestEnsureValidTempDir(t *testing.T) {
	t.Run("stale dir is unset", func(t *testing.T) {
		t.Setenv("TMPDIR", t.TempDir()+"/PKInstallSandbox.gone/tmp")
		ensureValidTempDir()
		if v, ok := os.LookupEnv("TMPDIR"); ok {
			t.Errorf("TMPDIR = %q, want unset", v)
		}
	})

	t.Run("dir that is actually a file is unset", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMPDIR", file)
		ensureValidTempDir()
		if v, ok := os.LookupEnv("TMPDIR"); ok {
			t.Errorf("TMPDIR = %q, want unset", v)
		}
	})

	t.Run("existing dir is left alone", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("TMPDIR", dir)
		ensureValidTempDir()
		if got := os.Getenv("TMPDIR"); got != dir {
			t.Errorf("TMPDIR = %q, want %q", got, dir)
		}
	})

	t.Run("unset TMPDIR is a no-op", func(t *testing.T) {
		unsetenvForTest(t, "TMPDIR")
		ensureValidTempDir()
		if _, ok := os.LookupEnv("TMPDIR"); ok {
			t.Error("TMPDIR became set")
		}
	})
}
