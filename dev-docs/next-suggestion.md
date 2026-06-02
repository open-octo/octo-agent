# After-turn follow-up suggestion

After each turn the TUI offers **one** LLM-generated follow-up — a next message
the user might send — as **ghost text** in the empty input box. `Tab` or `→`
accepts it (fills the input to edit or send); typing or starting a turn
dismisses it. On by default; `--no-suggest` / `OCTO_SUGGEST=0` turns it off.

## Generation

`Agent.Suggest(ctx)` (internal/agent) makes a throwaway provider call: it
appends a one-line instruction to a **snapshot** of history (never the live
`History`) and asks for a single next user message, capped at `suggestMaxTokens`.
Because it's a snapshot, the conversation isn't polluted; because the history
prefix is prompt-cached, only the instruction and the one-line reply are fresh
tokens. The result is passed through `cleanSuggestion` (first non-empty line,
list/quote decoration stripped). Its usage is intentionally **not** accrued into
the session — it's an auxiliary UI call, not part of a turn.

`Suggest` is provider-agnostic and lives in the agent package alongside the
`Sender`; it returns `""` (no error) when there's nothing to suggest.

## TUI wiring (cmd/octo)

- On a clean `turnEndedMsg` (no error/interrupt) and when `cfg.suggest` is set,
  the model dispatches `suggestCmd()` — a `tea.Cmd` that runs `Suggest` off the
  event loop and returns a `suggestionMsg` (nil on empty/error, which bubbletea
  ignores).
- `suggestionMsg` is applied only while **idle with an empty input**, so a stale
  suggestion never clobbers in-progress typing or a running turn.
- Ghost text reuses the textarea **placeholder**: `setSuggestion` swaps the
  idle "Ask anything…" hint for the suggestion (one line); `clearSuggestion`
  restores it. `acceptSuggestion` fills the value and clears.
- `handleKey` intercepts `Tab`/`→` to accept **only when the input is empty**;
  with text present those keys keep their normal behaviour. Starting a turn
  (`startTurnEcho`) clears any pending suggestion.

Plain/headless mode is untouched: the suggestion is a TUI ghost-text affordance,
and the plain path has nowhere to render it.

## Constraints

- Only generated in the interactive TUI; one suggestion at a time.
- No persistence; no extra model call when disabled.
- Does not change `agent.AgentEvent` or the turn loop — it's a post-turn side
  call plus input-box rendering.
