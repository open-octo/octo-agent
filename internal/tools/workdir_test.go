package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkingDirOrCWD_PrefersStampedDir(t *testing.T) {
	ctx := WithWorkingDir(context.Background(), "/some/stamped/dir")
	if got := WorkingDirOrCWD(ctx); got != "/some/stamped/dir" {
		t.Errorf("got %q, want the stamped dir", got)
	}
}

func TestWorkingDirOrCWD_FallsBackToProcessCWD(t *testing.T) {
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Error(err)
		}
	})

	got, err := filepath.EvalSymlinks(WorkingDirOrCWD(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got %q, want the process CWD %q", got, want)
	}
}
