---
title: Slash commands
description: Every / command, and how they differ across the TUI, Web UI, and IM channels.
---

There is no single, shared command set — the TUI, the Web UI's typed chat, and IM channels each
recognize a different list, because each surface has different session semantics (the TUI's session
is the process; an IM chat can be re-bound to a different session entirely).

## TUI

| Command | Syntax | Behavior |
|---|---|---|
| `/help` | `/help` | Prints the command list and key hints |
| `/model` | `/model <name>` | Switches to another configured model by name — its endpoint's provider and connection come with it — and rebuilds the toolset for it |
| `/thinking` | `/thinking off\|low\|medium\|high\|xhigh\|max` | Sets reasoning effort; rebuilds the sender, since the thinking budget is set at construction time |
| `/compact` | `/compact` | Compacts history now; refused while a turn is running |
| `/clear` | `/clear` | Wipes history and saves immediately; refused mid-turn. Keeps the same session file — for a brand-new one, see `/new` below (IM only) |
| `/goal` | see [Run long-horizon goals](/docs/guides/goals/) | `/goal edit` here is prefill-only — see the note on that page |
| `/loop` | `/loop [interval] <task>` | Not a router command: the message passes through to the model, which handles the convention via the `schedule_wakeup` tool — see [Run a recurring loop](/docs/guides/loop/) |
| `/skills` | `/skills` | Lists discovered skills with their source and description |
| `/mcp` | `/mcp` | Lists connected MCP servers — tool/resource/prompt counts, server instructions |
| `/workflows` | `/workflows` | Lists named workflows (embedded, user, and project); run one by describing the task, not by slash-invoking it |
| `/memory` | `/memory` | Lists files under the memory directory with sizes |
| `/init` | `/init` | Runs a full tool-enabled turn that generates or updates `.octorules` |
| `/save` | `/save` | Saves the session now, prints the file path |
| `/sessions` | `/sessions` | Lists the 10 most recent sessions |
| `/exit`, `/quit` | | Quits (same as Ctrl-C / Ctrl-D) |
| `/<skill-name>` | `/<name> [args]` | Any discovered skill not shadowed by a reserved command above is sent as ordinary `/<name>` text so the model loads it via the `skill` tool (needs tools — refused without them) |

Anything starting with `/` that isn't recognized is sent to the model as plain text — handy for
paths or regexes that happen to start with a slash.

## Web UI (typed chat)

Only three commands are parsed from typed chat text; everything else round-trips to the model as
plain text (model/skill/workflow switching in the Web UI happens through dedicated buttons and a
`/`-triggered picker menu, not through parsed command text):

| Command | Behavior |
|---|---|
| `/clear` | Wipes the session's messages, saves, drops the cached agent and memory latch, broadcasts a history reload |
| `/compact` | Compacts in the background (registered so a stop/interrupt can cancel it) |
| `/goal [...]` | Same shared implementation IM uses — `/goal edit <text>` works inline here |

The composer's own `/` autocomplete is a picker for skills, workflows, and MCP tools/servers — not
a fixed command list. Selecting an entry just fills `/<name> ` into the box, which then triggers the
same server-side skill dispatch the TUI uses.

## IM channels

IM sessions can be re-bound between chats, so this surface has commands the others don't:

| Command | Syntax | Behavior |
|---|---|---|
| `/bind` | `/bind [--force] <number\|id>` | Attaches this chat to an existing session, by the index shown in `/list` or a short/full session id. History is preserved. `--force` steals a binding held by another chat past its lease |
| `/unbind` | `/unbind` | Detaches this chat from its session without deleting anything |
| `/new` | `/new` | Creates a brand-new session and binds this chat to it — the one way to start fresh without touching an existing session's history |
| `/clear` | `/clear` | Wipes history but keeps the current binding |
| `/compact` | `/compact` | Compacts now, out-of-band so it doesn't block the chat |
| `/model` | `/model [name\|default]` | No argument lists the configured models; `/model <name>` binds the session to that model, `/model default` unbinds back to the default. The binding persists and is the same one the Web UI's model picker shows |
| `/goal [...]` | | Same shared implementation as Web — `/goal edit <text>` works inline |
| `/stop` | `/stop` | Interrupts the in-flight turn |
| `/status` | `/status` | Reports how long this chat has been bound, plus input/output token counts |
| `/list` | `/list` | Lists up to 20 saved sessions, numbered for `/bind` |

:::note[How IM resolves a `/` message]
Reserved commands (the table above) are handled by the command router and can't be shadowed.
`/loop` and any `/<skill-name>` that matches a discovered skill pass through to the model as
ordinary text, same as the TUI and Web surfaces. Only a slash token that matches neither returns
"Unknown command".
:::

`/bind`, `/unbind`, `/clear`, and `/new` all drop the chat's remembered-permission cache and memory
injector state, since those are scoped to the conversation that's being replaced — see
[Permissions](/docs/reference/permissions/).

## Availability at a glance

| Command | TUI | Web | IM |
|---|:-:|:-:|:-:|
| `/help` | ✓ | | |
| `/model` | ✓ | | ✓ |
| `/thinking` | ✓ | | |
| `/compact` | ✓ | ✓ | ✓ |
| `/clear` | ✓ | ✓ | ✓ |
| `/goal ...` | ✓ | ✓ | ✓ |
| `/skills` `/mcp` `/workflows` `/memory` `/save` `/sessions` `/init` | ✓ | | |
| `/exit` `/quit` | ✓ | | |
| `/<skill-name>` | ✓ | ✓ (via picker) | |
| `/bind` `/unbind` `/new` `/stop` `/status` `/list` | | | ✓ |

Next: session binding and re-binding is covered in more depth in
[Bridge to chat apps](/docs/guides/channels/).
