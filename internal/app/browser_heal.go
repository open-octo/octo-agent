package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/browser"
)

// makeBrowserHealer builds the LLM-backed step healer used by the browser tool's
// run_skill. When a recorded step fails (e.g. a drifted selector), it shows the
// model the page's current interactive elements (a text digest — model-agnostic,
// no vision needed when the DOM/AX is reachable) and asks for the corrected
// selector, which it writes into the step for retry + write-back.
//
// Returns nil when no sender is configured, so run_skill stays deterministic.
func makeBrowserHealer(sender agent.Sender, model string) browser.Healer {
	if sender == nil {
		return nil
	}
	return func(ctx context.Context, page *browser.Page, step *browser.Step, cause error) error {
		digest, err := browser.InteractiveDigest(ctx, page, step.Frame, 60)
		if err != nil {
			return fmt.Errorf("heal: digest: %w", err)
		}
		if len(digest) == 0 {
			return fmt.Errorf("heal: no interactive elements to match against")
		}
		var elems strings.Builder
		for _, d := range digest {
			fmt.Fprintf(&elems, "%s\t%s\n", d.Selector, d.Text)
		}
		const system = "You repair a failed browser-automation step. Given the intended action and the page's current interactive elements (each line: CSS_SELECTOR<TAB>visible text), reply with ONLY the single best CSS selector for the intended element. Reply NONE if nothing matches. No prose, no backticks."
		user := fmt.Sprintf("Intended action: %s\nIntended element label: %q\nOld selector (no longer matches): %s\n\nCurrent elements:\n%s",
			step.Action, step.Label, step.Selector, elems.String())

		reply, err := sender.SendMessages(ctx, model, system, []agent.Message{
			{Role: agent.RoleUser, Content: user},
		}, 256)
		if err != nil {
			return fmt.Errorf("heal: model: %w", err)
		}
		sel := strings.TrimSpace(reply.Content)
		if i := strings.IndexByte(sel, '\n'); i >= 0 {
			sel = sel[:i]
		}
		sel = strings.Trim(sel, "`\" ")
		if sel == "" || strings.EqualFold(sel, "NONE") {
			return fmt.Errorf("heal: model could not identify a replacement selector")
		}
		step.Selector = sel
		return nil
	}
}
