package tools

import (
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/browser"
)

// TestRenderBrowserRecordingsManifest: an empty recordings dir renders nothing; a
// recording renders its name, description, params, and outputs plus the replay
// invocation note.
func TestRenderBrowserRecordingsManifest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OCTO_BROWSER_SKILLS_DIR", dir)

	if m := RenderBrowserRecordingsManifest(); m != "" {
		t.Fatalf("empty dir should render empty, got %q", m)
	}

	if err := browser.SaveRecording(dir+"/download-excels.yaml", browser.Recording{
		Name:        "download-excels",
		Description: "download the monthly reports",
		Params:      []browser.Param{{Name: "month"}},
		Outputs:     []browser.Output{{Name: "files", Type: "file[]"}},
		Steps:       []browser.Step{{Action: "navigate", URL: "x"}},
	}); err != nil {
		t.Fatal(err)
	}
	// A second recording whose output declares no type — it should render as a
	// bare name (no parens), exercising the type-less branch. It also has no
	// description, so the manifest must fall back to a step digest: without one
	// the model only sees the name and replays near-miss recordings (a recording
	// named for item 1 got replayed for a request about item 2).
	if err := browser.SaveRecording(dir+"/grab-id.yaml", browser.Recording{
		Name:    "grab-id",
		Outputs: []browser.Output{{Name: "raw"}},
		Steps: []browser.Step{
			{Action: "navigate", URL: "https://example.com/hot"},
			{Action: "click", Selector: "#a", Label: "第一条"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	m := RenderBrowserRecordingsManifest()
	for _, want := range []string{
		"# Browser recordings", "action=replay", "download-excels",
		"download the monthly reports", "[params: month]", "[outputs: files (file[])]",
		"grab-id", "[outputs: raw]",
		// the all-or-nothing usage boundary
		"verbatim", "drive the browser directly",
		// the digest fallback for a description-less recording
		`grab-id: steps: navigate example.com/hot → click "第一条"`,
	} {
		if !strings.Contains(m, want) {
			t.Fatalf("manifest missing %q:\n%s", want, m)
		}
	}
	if strings.Contains(m, "raw (") {
		t.Fatalf("type-less output must render as a bare name, got:\n%s", m)
	}
}

// TestSkillsManifestIncludesBrowserRecordings: the combiner appends the browser
// section even when there are no SKILL.md skills (a nil registry renders "").
func TestSkillsManifestIncludesBrowserRecordings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OCTO_BROWSER_SKILLS_DIR", dir)
	if err := browser.SaveRecording(dir+"/dl.yaml", browser.Recording{
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
