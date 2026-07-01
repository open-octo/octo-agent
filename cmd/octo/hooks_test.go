package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunHooksList_ShowsUserConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("OCTO_HOOK_PRE_TURN", "")
	t.Setenv("OCTO_HOOK_POST_TURN", "")

	dir := filepath.Join(home, ".octo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "hooks:\n  Stop:\n    - command: \"retain\"\n      async: true\n  PostToolUse:\n    - matcher: \"terminal\"\n      command: \"audit\"\n"
	if err := os.WriteFile(filepath.Join(dir, "hooks.yml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if code := runHooks([]string{"list"}, &out, &out); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	s := out.String()
	for _, want := range []string{"Stop", "retain", "(async)", "PostToolUse", "audit", "matcher=terminal"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q in:\n%s", want, s)
		}
	}
}

func TestRunHooks_UnknownSubcommand(t *testing.T) {
	var out bytes.Buffer
	if code := runHooks([]string{"frobnicate"}, &out, &out); code != 2 {
		t.Errorf("unknown subcommand exit = %d, want 2", code)
	}
}
