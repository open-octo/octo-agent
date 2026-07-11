---
title: History compaction
description: How octo keeps a long session inside the context window without losing what matters.
---

Long sessions get compacted rather than truncated blindly. There are two triggers, a cheap tier
that runs before any LLM call, and a reactive path for when a provider rejects a request outright.

## When it triggers

Auto-compaction uses a **single threshold** — 75% of the model's context window by
default (`--compact-threshold`; set the percentage via `--compact-auto-pct` or
`compact_auto_pct`, `<0` disables, `>0` is an explicit token count). That one
threshold is checked at two safe boundaries:

- **Before each message to the provider** (between turns).
- **After each tool-call batch**, before the next provider call within the same
  turn — so context growth inside a long agentic turn is folded at the same
  threshold instead of quietly climbing past it until the next turn.

Token counts prefer the provider's actual reported usage over octo's own
estimate, so the trigger tracks reality once a real number is available.

## What gets compacted first

Two tiers, always in this order, whether triggered automatically or run manually via `/compact`:

1. **Reclaim stale tool results — no LLM call.** The 6 most recent `tool_result` blocks are always
   kept verbatim. Any older one larger than 4KB gets its content replaced with a placeholder noting
   it was elided and can be re-run to see again. If this alone drops estimated tokens below the
   trigger, compaction stops here — no summarize call, no cost.
2. **Summarize the oldest turns — one LLM call.** Only if still over trigger after reclamation.
   Walking from newest to oldest, octo keeps adding whole user turns to a "kept" tail until it would
   exceed a keep-budget (30% of the window, additionally capped at half of the trigger threshold
   itself). Everything older gets folded into one summary. The split never happens mid-turn, so a
   tool call and its result are never separated.

Small, recent tool output is never touched regardless of how the session grows — only the
old-and-large combination gets reclaimed.

## The anti-thrash guard

If the portion that *would* be folded is less than 15% of total history, the automatic path skips
summarizing entirely rather than spending an LLM call to fold a sliver — the sign that the real bulk
is sitting in the kept tail (one huge tool-heavy turn), not in old history reclamation would
otherwise have to run again almost every subsequent turn for negligible benefit.

This guard is automatic-only: a manual `/compact` always attempts the fold, on the assumption that
if you asked for it, a small gain is still a gain.

## Recovering from a context-length error

If a send fails because the provider rejects the request as too long, octo retries within the same
turn, escalating only as far as needed:

1. **Reclaim, then retry as-is** — if the error names the deficit (many providers do) and reclamation
   alone freed enough headroom, retry immediately with no summarize call.
2. **Pop one message, summarize, retry** — drops just the newest message (preserving as much of the
   provider's prompt cache as possible), compacts the rest, reattaches the popped message.
3. **Pop about half of history, summarize, retry** — only if step 2 still doesn't fit; sacrifices the
   cache to guarantee the retry succeeds.

This is reactive and bounded to one attempt per turn — unlike the proactive triggers above, it fires
only after a real failure, and it targets the parsed exact deficit rather than the general 30%
keep-budget.

## Where folded history goes

When a summarize call runs (either trigger, or a manual `/compact`) inside a persisted session — the
CLI or the Web UI, not headless one-shots or IM — the original messages being folded are archived as
a readable Markdown transcript before they're replaced by the summary. The summary that remains in
history gets a note pointing at the archive path, so the model can `read` the file back later if a
detail turns out to matter. Reclamation (tier 1 above) and overflow recovery don't archive — only an
actual summarize call does.

## What you'll see

`/compact` and automatic compaction both stream the same three events (start, progress, done) on
every transport, so the TUI, Web UI, and IM channels show identical behavior:

- Reclamation alone: `✦ reclaimed stale tool output · ~Xk → ~Yk tokens`
- A summarize happened: `✦ compacted context · folded N message(s) · ~Xk → ~Yk tokens`
- No measurable reduction: nothing is printed at all, rather than claiming work that didn't help.

## Controlling the cost

Set `lite_model` in [`config.yml`](/docs/reference/config-file/) and the summarize call runs on that
cheaper model first, falling back to your primary model only if the lite call fails — the main lever
for keeping compaction's own token cost down on a long-running session.

Next: an error mid-turn from an over-length *reply* (not the input) is a different mechanism — see
[The agent loop](/docs/concepts/agent-loop/#recovering-from-a-truncated-reply).
