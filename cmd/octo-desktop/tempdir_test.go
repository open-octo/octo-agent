package main

import (
	"os"
	"testing"
)

func TestEnsureValidTempDir(t *testing.T) {
	t.Run("stale dir is unset", func(t *testing.T) {
		t.Setenv("TMPDIR", t.TempDir()+"/PKInstallSandbox.gone/tmp")
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
		os.Unsetenv("TMPDIR")
		ensureValidTempDir()
		if _, ok := os.LookupEnv("TMPDIR"); ok {
			t.Error("TMPDIR became set")
		}
	})
}
