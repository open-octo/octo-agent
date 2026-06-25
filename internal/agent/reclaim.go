package agent

import "fmt"

// hotToolResults is how many of the most recent tool_result blocks reclamation
// keeps inline verbatim. Older results are candidates for eliding — the model
// rarely needs the raw bytes of a tool call it made many steps ago.
const hotToolResults = 6

// staleToolResultMinBytes is the size below which a stale tool_result is left
// alone: small results aren't worth the cache cost of a rewrite.
const staleToolResultMinBytes = 4_000

// reclaimStaleToolResults rewrites old, large tool_result blocks to a compact
// placeholder, freeing context without an LLM call. This is the cheap tier that
// shrinks a single huge agentic turn — the case the summarize path deliberately
// skips (one user turn holding ~150k of tool output can't be folded on a
// user-turn boundary, but its stale tool results can be elided in place).
//
// The most recent hotToolResults tool results are kept inline verbatim; older
// ones whose Result exceeds staleToolResultMinBytes are elided, preserving the
// tool_use pairing (ToolUseID/IsError) so the wire structure stays valid. The
// elided originals are regenerable by re-running the tool — the placeholder
// says so, mirroring the write-time microCompact backstop.
//
// Returns the estimated tokens reclaimed (0 if nothing changed). It rebuilds
// history via ReplaceAll, which invalidates the prompt-cache prefix from the
// first rewrite — still far cheaper than an LLM summarize.
func (a *Agent) reclaimStaleToolResults() int {
	msgs := a.History.Snapshot()

	// Map tool_use ID → tool name (for friendlier placeholders) and locate
	// every tool_result block in order.
	names := map[string]string{}
	type loc struct{ mi, bi int }
	var locs []loc
	for mi := range msgs {
		for bi, b := range msgs[mi].Blocks {
			switch b.Type {
			case "tool_use":
				names[b.ID] = b.Name
			case "tool_result":
				locs = append(locs, loc{mi, bi})
			}
		}
	}
	if len(locs) <= hotToolResults {
		return 0
	}

	// Snapshot copies Message structs but their Blocks slices still alias the
	// live history, so copy-on-write each message we touch before mutating.
	cloned := map[int]bool{}
	reclaimed := 0
	for _, l := range locs[:len(locs)-hotToolResults] {
		if len(msgs[l.mi].Blocks[l.bi].Result) < staleToolResultMinBytes {
			continue
		}
		if !cloned[l.mi] {
			cp := make([]ContentBlock, len(msgs[l.mi].Blocks))
			copy(cp, msgs[l.mi].Blocks)
			msgs[l.mi].Blocks = cp
			cloned[l.mi] = true
		}
		b := &msgs[l.mi].Blocks[l.bi]
		name := names[b.ToolUseID]
		if name == "" {
			name = "tool"
		}
		freed := estimateText(b.Result)
		b.Result = fmt.Sprintf(
			"[%d bytes elided by octo to save context — earlier %s result; re-run the tool to view it again]",
			len(b.Result), name)
		b.UI = nil // the structured card no longer matches the elided text
		reclaimed += freed - estimateText(b.Result)
	}
	if reclaimed <= 0 {
		return 0
	}
	a.History.ReplaceAll(msgs)
	return reclaimed
}
