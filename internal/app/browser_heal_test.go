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

// TestAcceptHealReply: the model's answer is taken only when it is a selector
// the prompt offered — cleaned of the usual wrapping (backticks, a trailing
// text column copied from the digest line) — and anything else is refused
// rather than acted on: NONE/empty as "no match", an off-list selector (the
// tail of the dead selector echoed back, #2404) as an invented one.
func TestAcceptHealReply(t *testing.T) {
	cands := []browser.DigestElement{{Selector: "#q", Text: "Search"}, {Selector: "div > span:nth-of-type(2)", Text: "笔记管理"}}
	cases := []struct {
		reply   string
		want    string
		wantErr string
	}{
		{reply: "#q", want: "#q"},
		{reply: "`#q`\n", want: "#q"},
		{reply: "div > span:nth-of-type(2)\t笔记管理", want: "div > span:nth-of-type(2)"},
		{reply: "NONE", wantErr: "could not identify"},
		{reply: "", wantErr: "could not identify"},
		{reply: "span.title-wrapper", wantErr: "not one of the page's current elements"},
		{reply: "#q, #other", wantErr: "not one of the page's current elements"},
	}
	for _, c := range cases {
		got, err := acceptHealReply(c.reply, cands)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("reply %q: want error containing %q, got %v (sel %q)", c.reply, c.wantErr, err, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("reply %q: want %q, got %q (err %v)", c.reply, c.want, got, err)
		}
	}
}

// TestHealCandidatesLabelFirst: label-matched elements lead the list and an
// element present in both lists appears once, at its label-matched position.
func TestHealCandidatesLabelFirst(t *testing.T) {
	byText := []browser.DigestElement{{Selector: "div > span", Text: "笔记管理"}}
	digest := []browser.DigestElement{{Selector: "#a", Text: "A"}, {Selector: "div > span", Text: "笔记管理"}, {Selector: "", Text: "no selector"}}
	got := healCandidates(byText, digest)
	if len(got) != 2 || got[0].Selector != "div > span" || got[1].Selector != "#a" {
		t.Fatalf("want [div > span, #a], got %+v", got)
	}
}
