package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/hooks"
	"github.com/open-octo/octo-agent/internal/prompt"
)

// Mounted folders' hooks load without a per-folder trust flag — mounting was
// the grant — and a task (no mounts) loads exactly what it always did.
func TestSourceDirHooks_MountIsTheTrustGrant(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("OCTO_HOOK_PRE_TURN", "")
	t.Setenv("OCTO_HOOK_POST_TURN", "")

	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, ".octo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".octo", "hooks.yml"), []byte("hooks:\n  Stop:\n    - command: \"echo hi\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cwd := t.TempDir() // the workspace: carries no hooks of its own
	if e := hooks.EngineFromEnvAndFiles(hooks.NewSeenSet(), cwd, true, src); !e.Configured(hooks.EventStop) {
		t.Error("a mounted folder's hooks did not load")
	}
	if e := hooks.EngineFromEnvAndFiles(hooks.NewSeenSet(), cwd, true); e.Configured(hooks.EventStop) {
		t.Error("hooks loaded from a folder that was never mounted")
	}
}

// Mounted folders' .octorules layer into the composed prompt after the cwd's
// own, each headed by its folder so multi-repo rules stay attributable.
func TestCompose_MountedFolderRules(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, ".octorules"), []byte("never push to main"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := prompt.Compose("", t.TempDir(), "", "", "", "", false, false, src)
	if !strings.Contains(got, "never push to main") {
		t.Error("mounted folder's .octorules missing from the composed prompt")
	}
	if !strings.Contains(got, src) {
		t.Error("the rules layer does not name which folder it came from")
	}

	without := prompt.Compose("", t.TempDir(), "", "", "", "", false, false)
	if strings.Contains(without, "never push to main") {
		t.Error("rules leaked into a compose that mounted nothing")
	}
}

// The env context says which mounted folders contribute rules/hooks.
func TestAppendProjectEnvContext_MarksLoadedSources(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, ".octorules"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	proj := &sessionGroup{ID: "g-1", WorkingDir: t.TempDir(), SourceDirs: []string{src}}
	got := appendProjectEnvContext("base\n", proj)
	if !strings.Contains(got, "[loads .octorules]") {
		t.Errorf("missing [loads .octorules] marker:\n%s", got)
	}
}
