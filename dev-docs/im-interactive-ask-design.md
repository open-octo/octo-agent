# IM Interactive Permission Ask

IM channel turns confirm ask-class tool calls the same way web turns do —
interactively — instead of denying them outright. When a tool call resolves
to an ask verdict, the agent sends a confirmation prompt into the chat and
the session's next plain message is consumed as the answer. This brings IM
to permission parity with the web UI's confirmation modal.

## Goals

- Ask-class verdicts (sudo commands, `restart_server`, anything without an
  allow rule) become answerable from DingTalk/Feishu/Telegram/Discord/WeCom/
  Weixin chats.
- Fail closed: silence, ambiguity, cancellation, and timeout all deny.
- Same permission *policy* as everywhere else — only the prompt transport
  differs.

## Non-goals

- No platform buttons or interactive cards. Plain text works identically on
  all six platforms; buttons would need six adapter implementations for a
  marginal UX gain.
- No "remember this decision". Chat approvals are one-shot — in a group
  chat, a remembered allow would outlive the person who granted it.

## Answer = the next message

The asking turn blocks inside the tool loop, holding the session's `runMu`.
The answer therefore cannot travel through the normal turn path — a new turn
would queue behind `BeginRun` waiting for the very turn that is asking, a
deadlock. Instead:

- `channel.Session` carries a single ask slot (`ask.go`): `BeginAsk` claims
  it and returns a reply channel; `DeliverAskReply` routes text to it and
  reports whether it was consumed. One ask at a time, one reply per ask.
- The server's inbound dispatcher (`routeChannelEvent`) checks, in order:
  slash commands (so `/stop` still cancels the asking turn), then a pending
  ask (the message is the answer, consumed inline off the adapter read
  loop), then the normal turn path.

A known, accepted race: a genuine steer message that arrives exactly while
an ask is pending is consumed as the answer. A non-affirmative answer just
denies — the cost is one denied tool call and a resent message.

## The prompt and the answer

`channelPermissionAsk` (server) sends:

```
⚠️ Allow <tool>? Reply yes / 允许 to approve — any other reply denies.
(Auto-deny in 5m0s; /stop cancels the task.)
```

Approval requires an explicit affirmative — one of `yes y ok allow 是 可以
同意 允许` (case-insensitive, trimmed). Everything else denies: arbitrary
replies, the 5-minute timeout, and turn cancellation (`/stop` cancels the
turn ctx, which the ask select observes). `remember` is always false.

## Per-turn gate

`handleChannelMessage` builds a fresh gate for every IM turn — engine with
the configured permission mode (`resolvePermissionMode`, same as web) plus
`channelPermissionAsk` — and sets it on the session agent after `BeginRun`
(turns are serialised, so this cannot race a running turn). This replaces
the old factory-time gate, which froze a hard-coded strict-mode policy
snapshot at session creation and — via its `gate, _ :=` construction —
silently ran turns *ungated* when engine construction failed. An engine
failure now aborts the turn with an error reply in the chat.

## Components

| Piece | Where | What |
|---|---|---|
| Ask slot | `internal/channel/ask.go` | `Session.BeginAsk` / `DeliverAskReply`, one pending ask per session |
| Chat ask | `internal/server/channel_ask.go` | prompt text, affirmative set, 5-minute timeout, ctx-cancel deny |
| Routing | `internal/server/server.go` `routeChannelEvent` | command → pending ask → turn, in that order |
| Per-turn gate | `internal/server/server.go` `handleChannelMessage` | configured mode + chat ask, engine failure aborts the turn |

## Testing

- Ask slot: claim/deliver/release/one-reply-only/second-begin-refused unit
  tests in `internal/channel`.
- Chat ask: affirmative table, allow/deny/cancel/timeout paths against a
  fake adapter.
- Routing: pending ask consumes the message, `/stop` bypasses the ask, a
  normal message runs a full stub-sender turn.
- Gate: end-to-end policy check (allow without prompt, ask prompts then
  follows the reply, hard-deny never prompts).
