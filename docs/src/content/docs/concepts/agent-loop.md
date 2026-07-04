---
title: The agent loop
description: Tools, permission gating, streaming, and compaction, in one mental model.
---

Every octo interface — CLI, web, IM — drives the same loop underneath:

1. **Send** the conversation (history + system prompt) to the provider.
2. The model replies with text, or a **tool call**.
3. A tool call goes through the **permission engine** (allow / ask / deny rules) before it runs.
4. The tool executes; its result is appended to history as a `tool_result` block.
5. Repeat from step 1 until the model replies with plain text and no further tool calls.

## Streaming

Both providers stream by default — text deltas and tool-call argument fragments arrive
incrementally and are re-aggregated before dispatch. `--stream=false` buffers a turn and prints only
the final text, which is what you want when capturing output into a file or a script.

## Permission gating

Every tool call is visible and gated — nothing runs silently. Rules can allow, ask, or deny by tool
name and argument pattern; a [`PreToolUse` hook](/docs/guides/hooks/) can add stricter gates on top
of, but never looser than, the configured rules.

## History compaction

Long sessions get compacted rather than truncated blindly: octo reclaims context from stale tool
results before it evicts actual conversation turns, and recovers gracefully from a provider's
context-length error mid-turn rather than losing the whole session. This is a large enough topic on
its own — see [History compaction](/docs/concepts/compaction/) for the full mechanism.

## Recovering from a truncated reply

A different problem from running out of input context: the provider's *reply* itself hits the
output token cap mid-thought. octo escalates in up to two ways before giving up cleanly:

1. **Retry the same round at a higher cap.** Both providers default to a 16,384-token first
   attempt; if that's truncated, one retry goes out at an escalated 32,768-token cap
   (`--max-tokens-escalate`, `0` disables it), replayed from unchanged history rather than
   appending the cut-off reply.
2. **If still truncated and the reply is plain text (no tool call in progress),** octo appends what
   was written so far, adds a fixed instruction to continue exactly where it left off, and keeps
   going — up to 3 such continuations per turn.

A truncated reply that was in the middle of a tool call never retries this way — a tool call either
completed or it didn't — and stops the turn gracefully instead.

Next: how the system prompt itself is assembled — see
[Configuration layers](/docs/concepts/configuration-layers/).
