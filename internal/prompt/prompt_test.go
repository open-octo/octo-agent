package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompose_BaseAlwaysPresent(t *testing.T) {
	out := Compose("", t.TempDir()) // empty user, no .octorules
	if !strings.Contains(out, "octo") {
		t.Errorf("composed prompt should contain the base identity:\n%s", out)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("base layer must never be empty")
	}
}

func TestCompose_LayersInOrder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ProjectContextFile), []byte("PROJECT_RULE_X"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := Compose("USER_RULE_Y", dir)

	baseIdx := strings.Index(out, "octo")
	projIdx := strings.Index(out, "PROJECT_RULE_X")
	userIdx := strings.Index(out, "USER_RULE_Y")

	if baseIdx == -1 || projIdx == -1 || userIdx == -1 {
		t.Fatalf("all three layers should be present:\n%s", out)
	}
	// Order: base < project < user.
	if !(baseIdx < projIdx && projIdx < userIdx) {
		t.Errorf("layer order wrong: base=%d project=%d user=%d", baseIdx, projIdx, userIdx)
	}
	if !strings.Contains(out, ProjectContextFile) {
		t.Errorf("project layer should be labelled with the source file")
	}
}

func TestCompose_SkipsAbsentLayers(t *testing.T) {
	// No .octorules, no user prompt → just the base, no stray separators.
	out := Compose("", t.TempDir())
	if strings.Contains(out, "---") {
		t.Errorf("single-layer prompt should have no separator:\n%s", out)
	}
}

func TestReadProjectContext_MissingFileIsEmpty(t *testing.T) {
	if got := readProjectContext(t.TempDir()); got != "" {
		t.Errorf("missing .octorules should yield empty, got %q", got)
	}
	if got := readProjectContext(""); got != "" {
		t.Errorf("empty cwd should yield empty, got %q", got)
	}
}
