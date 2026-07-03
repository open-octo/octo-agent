---
title: Automate with hooks
description: Run your own shell commands at fixed points in the agent lifecycle.
---

Hooks run an external command at a fixed lifecycle point — Claude Code's hook model, ported to
every octo transport (CLI, web, IM).

## The seven events

| Event | Fires | Can it block? |
|---|---|---|
| `SessionStart` | once per logical session opening | stdout folds into context |
| `UserPromptSubmit` | before each user turn | stdout folds into context |
| `PreToolUse` | before each tool dispatch | yes — can allow/block the call |
| `PostToolUse` | after each successful tool result | stdout folds into context |
| `Stop` | when an assistant turn ends, success or error | side-effect only |
| `SubagentStop` | when a spawned sub-agent finishes | side-effect only |
| `PreCompact` | before history compaction | side-effect only |

`PreToolUse` is the only gate in the strict sense — its exit code and JSON decision can allow, ask,
or deny a tool call before it runs, composing with (and able to tighten) the normal permission
engine. The others are observation/side-effect points; `SessionStart`, `UserPromptSubmit`, and
`PostToolUse` additionally get their stdout folded back into the model's context.

## Where they're configured

Hooks are declared per event, matched by tool name pattern where relevant (`PreToolUse` /
`PostToolUse`), and run the same way across the CLI, `octo serve`'s web sessions, and every IM
channel — one engine, all transports.

Next: a common pairing is a `PostToolUse` hook on `terminal` that nudges a memory save after
`git commit` — see [Give it memory](/docs/guides/memory/).
