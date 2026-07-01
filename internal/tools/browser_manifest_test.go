package tools

import (
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/browser"
)

// TestRenderBrowserSkillsManifest: an empty recordings dir renders nothing; a
// recording renders its name, description, params, and outputs plus the run_skill
// invocation note.
func TestRenderBrowserSkillsManifest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OCTO_BROWSER_SKILLS_DIR", dir)

	if m := RenderBrowserSkillsManifest(); m != "" {
		t.Fatalf("empty dir should render empty, got %q", m)
	}

	if err := browser.SaveSkill(dir+"/download-excels.yaml", browser.Skill{
		Name:        "download-excels",
		Description: "download the monthly reports",
		Params:      []browser.Param{{Name: "month"}},
		Outputs:     []browser.Output{{Name: "files", Type: "file[]"}},
		Steps:       []browser.Step{{Action: "navigate", URL: "x"}},
	}); err != nil {
		t.Fatal(err)
	}
	m := RenderBrowserSkillsManifest()
	for _, want := range []string{
		"# Browser recordings", "run_skill", "download-excels",
		"download the monthly reports", "[params: month]", "[outputs: files (file[])]",
	} {
		if !strings.Contains(m, want) {
			t.Fatalf("manifest missing %q:\n%s", want, m)
		}
	}
}

// TestSkillsManifestIncludesBrowserRecordings: the combiner appends the browser
// section even when there are no SKILL.md skills (a nil registry renders "").
func TestSkillsManifestIncludesBrowserRecordings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OCTO_BROWSER_SKILLS_DIR", dir)
	if err := browser.SaveSkill(dir+"/dl.yaml", browser.Skill{
		Name:  "dl",
		Steps: []browser.Step{{Action: "navigate", URL: "x"}},
	}); err != nil {
		t.Fatal(err)
	}
	m := SkillsManifest(nil)
	if !strings.Contains(m, "# Browser recordings") || !strings.Contains(m, "- dl") {
		t.Fatalf("SkillsManifest should include browser recordings with a nil registry:\n%s", m)
	}
}
