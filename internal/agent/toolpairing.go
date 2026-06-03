package agent

// hasToolUse reports whether m contains any tool_use block.
func hasToolUse(m Message) bool {
	for _, b := range m.Blocks {
		if b.Type == "tool_use" {
			return true
		}
	}
	return false
}

// blocksOf returns m's content blocks, lifting a plain-text Content message into
// a single text block so callers can treat every message uniformly.
func blocksOf(m Message) []ContentBlock {
	if len(m.Blocks) > 0 {
		return m.Blocks
	}
	if m.Content != "" {
		return []ContentBlock{NewTextBlock(m.Content)}
	}
	return nil
}

// normalizeToolHistory repairs structural corruption in the message log that a
// provider would reject, rewriting History in place only if something changed.
// It is a defensive companion to ensureToolPairing: well-formed single-threaded
// history never triggers it, but if two turn goroutines ever briefly overlap and
// interleave their History.Append calls (the cae91036 incident), the log can end
// up with two consecutive assistant messages or a tool_use answered twice — both
// of which 400 the next send and permanently wedge the session.
func (a *Agent) normalizeToolHistory() {
	msgs := a.History.Snapshot()
	repaired, changed := normalizeMessages(msgs)
	if changed {
		a.History.ReplaceAll(repaired)
	}
}

// normalizeMessages returns msgs with two structural repairs applied, plus
// whether anything changed. Pure (no History access) so it is unit-testable.
//
//   - Consecutive assistant messages are coalesced into one. The wire format
//     requires an assistant tool_use message to be immediately followed by the
//     tool_result(s) answering it; an assistant message followed by another
//     assistant message orphans the first one's tool calls (HTTP 400
//     "tool_call_ids did not have response messages").
//   - Duplicate tool_result blocks sharing a tool_use_id are dropped, keeping
//     the first occurrence. A single tool_use answered twice is also rejected.
//
// New backing arrays are allocated for any message it rewrites, so the caller's
// snapshot (which shares block slices with the live History) is never mutated.
func normalizeMessages(msgs []Message) ([]Message, bool) {
	if len(msgs) == 0 {
		return msgs, false
	}
	changed := false

	// Pass 1 — coalesce consecutive assistant messages.
	coalesced := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if n := len(coalesced); m.Role == RoleAssistant && n > 0 && coalesced[n-1].Role == RoleAssistant {
			prev := coalesced[n-1]
			merged := make([]ContentBlock, 0, len(blocksOf(prev))+len(blocksOf(m)))
			merged = append(merged, blocksOf(prev)...)
			merged = append(merged, blocksOf(m)...)
			coalesced[n-1] = Message{Role: RoleAssistant, Blocks: merged}
			changed = true
			continue
		}
		coalesced = append(coalesced, m)
	}

	// Pass 2 — drop duplicate tool_result blocks (keeping the first occurrence
	// per id), and drop any message left empty as a result. The duplicate may
	// live in its own message (a second goroutine appended a whole tool_result
	// message for an id another goroutine had already answered), so emptied
	// messages are removed rather than left as zero-block stragglers.
	seen := make(map[string]bool)
	out := make([]Message, 0, len(coalesced))
	for _, m := range coalesced {
		if len(m.Blocks) == 0 {
			out = append(out, m)
			continue
		}
		kept := make([]ContentBlock, 0, len(m.Blocks))
		dropped := false
		for _, b := range m.Blocks {
			if b.Type == "tool_result" {
				if seen[b.ToolUseID] {
					dropped = true
					continue
				}
				seen[b.ToolUseID] = true
			}
			kept = append(kept, b)
		}
		if !dropped {
			out = append(out, m)
			continue
		}
		changed = true
		if len(kept) == 0 && m.Content == "" {
			continue // message held only duplicate tool_results — drop it
		}
		m.Blocks = kept
		out = append(out, m)
	}

	return out, changed
}

// synthesizeInterruptedToolResults creates error tool_result blocks for each
// tool_use block in the given message. Used when a turn is interrupted and
// the tool_use has no matching tool_result (either because dispatchTools never
// ran, or because it failed to produce results before the interrupt).
//
// The error message "[Interrupted by user]" signals clearly to the LLM that
// the tool did not complete and should be retried if needed.
func synthesizeInterruptedToolResults(blocks []ContentBlock) []ContentBlock {
	var results []ContentBlock
	for _, b := range blocks {
		if b.Type == "tool_use" {
			results = append(results, NewToolResultBlock(b.ID, interruptNote, true))
		}
	}
	return results
}

// ensureToolPairing scans the history and fixes any orphaned tool_use blocks
// by synthesizing error tool_result blocks. This is a defensive check that
// runs before each send() call in runLoop to prevent Anthropic HTTP 400 errors
// ("tool_calls must be followed by tool messages").
//
// The function handles these cases:
//   - Orphaned assistant(tool_use) at the end of history (no following tool_result)
//   - Multiple consecutive tool_use messages without tool_results (rare edge case)
//
// IMPORTANT: If the last message is a non-tool-result user message (e.g., from
// inbox drain), the synthetic tool_results are MERGED into that message rather
// than appended as a new message. This preserves the Anthropic API requirement
// that tool_use must be immediately followed by tool_result.
//
// Synthesized tool_results use is_error=true with "[Tool execution was interrupted]"
// to signal clearly to the LLM that the tool did not complete.
//
// It first runs normalizeToolHistory to repair any structurally invalid log
// (consecutive assistant messages, duplicate tool_results) so the orphan scan
// below sees a clean message sequence.
func (a *Agent) ensureToolPairing() {
	a.normalizeToolHistory()

	msgs := a.History.Snapshot()
	if len(msgs) == 0 {
		return
	}

	// Scan for orphaned tool_use blocks. We need to track which tool_use IDs
	// have been answered by tool_result blocks in subsequent messages.
	//
	// Build a set of answered tool_use IDs from tool_result blocks.
	answered := make(map[string]bool)
	for _, m := range msgs {
		if m.Role == RoleUser {
			for _, b := range m.Blocks {
				if b.Type == "tool_result" {
					answered[b.ToolUseID] = true
				}
			}
		}
	}

	// Find orphaned tool_use blocks (those without a matching tool_result).
	var orphans []ContentBlock
	for _, m := range msgs {
		if m.Role == RoleAssistant {
			for _, b := range m.Blocks {
				if b.Type == "tool_use" && !answered[b.ID] {
					orphans = append(orphans, b)
				}
			}
		}
	}

	if len(orphans) == 0 {
		return
	}

	// Synthesize error tool_results for all orphaned tool_use blocks.
	var results []ContentBlock
	for _, b := range orphans {
		results = append(results, NewToolResultBlock(b.ID, "[Tool execution was interrupted or failed to complete]", true))
	}

	// Check if the last message is a non-tool-result user message (e.g., from
	// inbox drain). If so, we need to MERGE the synthetic tool_results into
	// that message to preserve the tool_use/tool_result pairing requirement.
	lastMsg := msgs[len(msgs)-1]
	if lastMsg.Role == RoleUser && !hasToolResult(lastMsg) {
		// Merge: replace the last user message with one that contains both
		// the original content AND the synthetic tool_results.
		// The tool_results must come FIRST to satisfy the API requirement.
		mergedBlocks := make([]ContentBlock, 0, len(results)+1)
		mergedBlocks = append(mergedBlocks, results...)

		// Add the original content as a text block
		if lastMsg.Content != "" {
			mergedBlocks = append(mergedBlocks, NewTextBlock(lastMsg.Content))
		}
		// Add any existing blocks from the last message
		mergedBlocks = append(mergedBlocks, lastMsg.Blocks...)

		// Replace the last message in history
		a.History.replaceLast(Message{
			Role:   RoleUser,
			Blocks: mergedBlocks,
		})
	} else {
		// No merge needed - just append as a new user message
		a.History.Append(NewToolResultMessage(results))
	}
}
