package app

import (
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/browser"
)

// TestHealPromptIncludesFieldHint: a type step's Label is empty (an input has
// no textContent), so the recorded field hint is the healer's only semantic
// clue to which field was intended — it must reach the prompt alongside the
// old selector and the element list.
func TestHealPromptIncludesFieldHint(t *testing.T) {
	step := &browser.Step{Action: "type", Selector: "form > input:nth-of-type(2)", Hint: "Search keywords"}
	digest := []browser.DigestElement{{Selector: "#q", Text: "Search keywords"}}
	p := healPrompt(step, digest)
	for _, want := range []string{"type", `"Search keywords"`, "form > input:nth-of-type(2)", "Field hint", "#q\tSearch keywords"} {
		if !strings.Contains(p, want) {
			t.Fatalf("heal prompt missing %q:\n%s", want, p)
		}
	}
}
