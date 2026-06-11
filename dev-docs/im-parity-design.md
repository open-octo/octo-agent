# IM Channel Parity

Four capabilities the web transport had and the IM channel lacked, closed in
one round: conversation persistence, per-turn memory freshness, mid-turn
steering, and an in-chat `ask_user_question`. Together with the interactive
permission ask (`im-interactive-ask-design.md`), IM turns now run with the
same session machinery as web turns — only the prompt/reply surface differs.

## Conversation persistence

IM sessions back onto the same store web sessions use: `agent.Session` JSONL
under `~/.octo/sessions`, written via `Session.Persist()` after every turn
and reloaded on session creation. Before this, IM history lived only in
process memory — and with self-restart making restarts routine (upgrades,
config changes), every restart wiped every conversation.

- **Deterministic store ID** (`internal/channel/persist.go`):
  `im-<sanitized key>-<fnv32>`, derived from the session key
  (`platform:chat:user`), so the first message after a restart finds its
  file without any extra mapping state. `Source: "channel"` marks these in
  the web session list; the title defaults to the key.
- **Restore is best-effort**: a corrupt or missing file degrades to a fresh
  conversation, never blocks the chat.
- **`/unbind` and `/bind` delete the store.** Their contracts ("history
  cleared", "start fresh") must hold across restarts too — without the
  delete, `/bind`'s new session would just rehydrate the history the user
  asked to drop. `/bind` also clears a leftover store when no live session
  exists.
- Persist failures are logged to the server console and never eat the reply
  the user already received.

## Per-turn memory freshness

`handleChannelMessage` recomposes the agent's system prompt (memory
injection included) at the start of every turn. Web turns always had this —
`buildAgent` runs per turn — but the IM factory composed the prompt once at
session creation, so memory written mid-session was invisible to IM until a
restart.

## Mid-turn steer

A message arriving while a turn is running rides that turn instead of
queueing a whole second turn behind `runMu` (web/CLI parity):

- `routeChannelEvent` checks `Session.IsRunning()` (after the command and
  pending-ask checks) and enqueues the text into the running turn's
  `Agent.Inbox`, which the agent loop drains between iterations.
- Items that arrive after the turn's final drain chain into follow-up turns
  inside `handleChannelMessage`, still under `BeginRun`, mirroring the web's
  `runAgentTurnLoop`.
- Known small race: a turn that finishes between the `IsRunning` check and
  the enqueue leaves the message in the Inbox until the chat's next turn —
  delayed, not lost. Same acceptance class as the ask-reply race.

## `ask_user_question` over chat

The asker is now turn-scoped: `tools.WithAsker(ctx, …)` beats the
process-global asker (the same ctx-scoping pattern as the sub-agent manager
and task store). The server's global asker is the WebSocket one; an IM turn
using it would broadcast the question to browser tabs the session doesn't
have and hang until `/stop`. IM turns stamp `chatAsker` instead:

```
❓ [scope] Which environment?
1. staging
2. production
Reply with a number — or free text for something else.
```

The answer arrives through the same session ask slot the permission prompt
uses (`Session.BeginAsk`, requester-scoped, one at a time): a number picks
its option, `1,3` selects several when multi-select, an exact label matches
its option, anything else returns as the free-text "Other" answer, and the
5-minute timeout cancels. Out-of-range numbers fall through to free text —
over chat, a re-prompt loop is worse than letting the model read the raw
reply.

## Components

| Piece | Where | What |
|---|---|---|
| Store | `internal/channel/persist.go` | deterministic ID, restore-or-init, `Persist`, store delete on /bind //unbind |
| Steer intake | `internal/server/server.go` `routeChannelEvent` | command → ask → steer-if-running → turn |
| Chained turns + persist + recompose | `internal/server/server.go` `handleChannelMessage` | leftover-inbox loop, per-turn `prompt.Compose`, post-turn `Persist` |
| Ctx asker | `internal/tools/ask_user_question.go` | `WithAsker` / `askerFrom`, global fallback |
| Chat asker | `internal/server/channel_ask.go` | numbered options, reply parsing, timeout |

## Testing

- Persistence: deterministic/safe ID, persist→restore across managers,
  append across turns, `/unbind` and `/bind` delete, end-to-end through
  `handleChannelMessage` with a stub sender.
- Steer: blocking-sender test pins enqueue-while-running and the chained
  drain reaching the model.
- Freshness: memory file written between two turns is visible to the second.
- Asker: ctx-beats-global, global fallback, reply parsing table, numbered
  pick end-to-end, timeout cancel.
