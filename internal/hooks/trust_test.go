package hooks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// tempHome points HOME/USERPROFILE at a sandbox so the trust store and user
// config path stay inside the test.
func tempHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	return tmp
}

func TestFingerprint_StableAndContentSensitive(t *testing.T) {
	a := Fingerprint([]byte("hooks: {}"))
	if a != Fingerprint([]byte("hooks: {}")) {
		t.Error("fingerprint must be stable for identical content")
	}
	if a == Fingerprint([]byte("hooks: {Stop: []}")) {
		t.Error("fingerprint must change when content changes")
	}
}

func TestTrustStore_RoundTrip(t *testing.T) {
	tempHome(t)
	path := "/some/repo/.octo/hooks.yml"
	fp := Fingerprint([]byte("x"))

	if IsTrusted(path, fp) {
		t.Fatal("nothing should be trusted initially")
	}
	if err := RecordTrust(path, fp); err != nil {
		t.Fatalf("RecordTrust: %v", err)
	}
	if !IsTrusted(path, fp) {
		t.Error("recorded fingerprint must be trusted")
	}
	// A changed file (new fingerprint) is no longer trusted.
	if IsTrusted(path, Fingerprint([]byte("y"))) {
		t.Error("a changed fingerprint must not be trusted")
	}
}

func writeProjectHooks(t *testing.T, cwd, body string) {
	t.Helper()
	dir := filepath.Join(cwd, ".octo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hooks.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEngineFromEnvAndFiles_ProjectLoadGatedByFlag(t *testing.T) {
	tempHome(t)
	t.Setenv("OCTO_HOOK_PRE_TURN", "")
	t.Setenv("OCTO_HOOK_POST_TURN", "")
	cwd := t.TempDir()
	writeProjectHooks(t, cwd, "hooks:\n  Stop:\n    - command: \"retain\"\n")

	// loadProject=false → project file ignored.
	if e := EngineFromEnvAndFiles(NewSeenSet(), cwd, false); e.Configured(EventStop) {
		t.Error("project hooks must not load when loadProject is false")
	}
	// loadProject=true → project file applied.
	if e := EngineFromEnvAndFiles(NewSeenSet(), cwd, true); !e.Configured(EventStop) {
		t.Error("project hooks must load when loadProject is true")
	}
}

func TestProjectConfigPath(t *testing.T) {
	if got := ProjectConfigPath("/repo"); got != filepath.Join("/repo", ".octo", "hooks.yml") {
		t.Errorf("ProjectConfigPath = %q", got)
	}
	if ProjectConfigPath("") != "" {
		t.Error("empty cwd → empty path")
	}
}

// Sanity: a project-level hook actually dispatches once loaded.
func TestEngineFromEnvAndFiles_ProjectHookRuns(t *testing.T) {
	tempHome(t)
	t.Setenv("OCTO_HOOK_PRE_TURN", "")
	t.Setenv("OCTO_HOOK_POST_TURN", "")
	cwd := t.TempDir()
	writeProjectHooks(t, cwd, "hooks:\n  UserPromptSubmit:\n    - command: \""+makeScript(t, "echo proj")+"\"\n")
	e := EngineFromEnvAndFiles(NewSeenSet(), cwd, true)
	if got := e.Inject(context.Background(), Payload{Event: EventUserPromptSubmit}); got != "proj" {
		t.Errorf("project UserPromptSubmit hook should run; got %q", got)
	}
}
