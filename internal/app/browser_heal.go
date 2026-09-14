package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/browser"
)

// MakeBrowserHealer builds the LLM-backed step healer used by the browser tool's
// replay action. When a recorded step fails (e.g. a drifted selector), it shows the
// model the page's current interactive elements (a text digest — model-agnostic,
// no vision needed when the DOM/AX is reachable) and asks for the corrected
// selector, which it writes into the step for retry + write-back.
//
// Returns nil when no sender is configured, so replay stays deterministic.
func MakeBrowserHealer(sender agent.Sender, model string) browser.Healer {
	if sender == nil {
		return nil
	}
	return func(ctx context.Context, page *browser.Page, step *browser.Step, cause error) error {
		digest, err := browser.InteractiveDigest(ctx, page, step.Frame, 60)
		if err != nil {
			return fmt.Errorf("heal: digest: %w", err)
		}
		// The elements carrying the step's recorded text, whatever their tag,
		// go first: a click-handled span/div menu item is not an interactive
		// control by any query, so without these the list can lack the right
		// answer entirely — and a model handed such a list guesses (#2404).
		if byText, lerr := browser.LabelDigest(ctx, page, step.Frame, step.Label, 8); lerr == nil {
			digest = healCandidates(byText, digest)
		}
		if len(digest) == 0 {
			return fmt.Errorf("heal: no interactive elements to match against")
		}
		reply, err := sender.SendMessages(ctx, model, healSystemPrompt, []agent.Message{
			{Role: agent.RoleUser, Content: healPrompt(step, digest)},
		}, 256)
		if err != nil {
			return fmt.Errorf("heal: model: %w", err)
		}
		sel, err := acceptHealReply(reply.Content, digest)
		if err != nil {
			return err
		}
		step.Selector = sel
		return nil
	}
}

// healCandidates merges the label-matched elements ahead of the interactive
// digest, dropping duplicates by selector, so the prompt lists each element
// once with the strongest candidates on top.
func healCandidates(byText, digest []browser.DigestElement) []browser.DigestElement {
	seen := make(map[string]bool, len(byText)+len(digest))
	out := make([]browser.DigestElement, 0, len(byText)+len(digest))
	for _, list := range [][]browser.DigestElement{byText, digest} {
		for _, d := range list {
			if d.Selector == "" || seen[d.Selector] {
				continue
			}
			seen[d.Selector] = true
			out = append(out, d)
		}
	}
	return out
}

// acceptHealReply extracts the model's selector and accepts it only when it is
// one the prompt offered. The same hard constraint the recording distiller
// applies (a selector absent from the capture is rejected): the model saw a
// finite list, so anything else is invention — observed as the tail segment of
// the dead selector echoed back — and replay must not act on it, let alone
// persist it. NONE and an empty reply mean the model found no match.
func acceptHealReply(reply string, cands []browser.DigestElement) (string, error) {
	sel := strings.TrimSpace(reply)
	if i := strings.IndexByte(sel, '\n'); i >= 0 {
		sel = sel[:i]
	}
	// A model that copies the whole digest line brings the text column along.
	if i := strings.IndexByte(sel, '\t'); i >= 0 {
		sel = sel[:i]
	}
	sel = strings.Trim(sel, "`\" ")
	if sel == "" || strings.EqualFold(sel, "NONE") {
		return "", fmt.Errorf("heal: model could not identify a replacement selector")
	}
	for _, c := range cands {
		if c.Selector == sel {
			return sel, nil
		}
	}
	return "", fmt.Errorf("heal: model proposed %q, which is not one of the page's current elements — refusing to guess", sel)
}

// healSystemPrompt / healPrompt ask the model for the single best replacement
// selector, given the step's intent and the page's current interactive
// elements. The prompt carries the step's visible label AND its field hint:
// for a type/select step the label is empty (an input has no textContent), so
// the hint — the placeholder/name/aria-label captured at record time — is the
// only semantic clue to which field the step meant.
const healSystemPrompt = "You repair a failed browser-automation step. Given the intended action and the page's current elements (each line: CSS_SELECTOR<TAB>visible text), reply with ONLY the single best CSS selector for the intended element, copied verbatim from the list — never a selector that is not on the list, and never a piece of the old selector. When an element fingerprint (role/tag/neighbor text) is provided, the answer must be consistent with it. Reply NONE if nothing on the list matches. No prose, no backticks."

func healPrompt(step *browser.Step, digest []browser.DigestElement) string {
	var elems strings.Builder
	for _, d := range digest {
		fmt.Fprintf(&elems, "%s\t%s\n", d.Selector, d.Text)
	}
	// The recorded fingerprint (when present) turns the task from an open guess
	// into constrained matching: the intended element's role/tag and the stable
	// text the user saw next to it.
	fingerprint := ""
	if a := step.Anchors; a != nil {
		fingerprint = fmt.Sprintf("Element fingerprint: role=%q tag=%q neighbor_text=%q\n", a.Role, a.Tag, a.NeighborText)
	}
	return fmt.Sprintf("Intended action: %s\nIntended element label: %q\nField hint: %q\n%sOld selector (no longer matches): %s\n\nCurrent elements:\n%s",
		step.Action, step.Label, step.Hint, fingerprint, step.Selector, elems.String())
}

// MakeRecordingGenerator builds the LLM-backed skill distiller for record_stop. It
// refines the deterministic baseline into a clean optimal-path skill, grounded
// in the captured selectors (the engine enforces the selector constraint).
// Returns nil when no sender is configured, so generation stays deterministic.
func MakeRecordingGenerator(sender agent.Sender, model string) browser.RecordingGenerator {
	if sender == nil {
		return nil
	}
	return func(ctx context.Context, system, user string) (string, error) {
		reply, err := sender.SendMessages(ctx, model, system, []agent.Message{
			{Role: agent.RoleUser, Content: user},
		}, 2048)
		if err != nil {
			return "", err
		}
		return reply.Content, nil
	}
}
