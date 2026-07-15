---
title: Tools
description: Every built-in tool octo gives the model.
---

Built-in tools are on by default (`--no-tools` disables all of them, including MCP and skill
execution). Every call goes through the permission engine before it runs.

## Filesystem & search

| Tool | Purpose |
|---|---|
| `read_file` | read a file, optionally a line range |
| `write_file` | create or overwrite a file |
| `edit_file` | apply a targeted find/replace edit |
| `glob` | find files by pattern |
| `grep` | search file contents |

## Shell

| Tool | Purpose |
|---|---|
| `terminal` | run a shell command (foreground or background) |
| `terminal_output` | read output from a running background command |
| `terminal_input` | write to a background command's stdin (POSIX-only, see [Compatibility](/docs/reference/compatibility/)) |
| `kill_shell` | stop a background command |

Backgrounding is explicit, chosen per call rather than inferred: `detached` (an untracked daemon
that outlives octo entirely), `run_in_background: "async"` (tracked, one-shot — its completion is
pushed automatically, and `terminal_output`/`terminal_input` don't apply to it), or
`run_in_background: "interactive"` (tracked and long-running, readable and writable via those two
tools).

Anything not backgrounded runs **synchronously with a timeout** — 120 seconds by default, or set
`timeout` (whole seconds, up to a 600s ceiling; a larger value is rejected with a pointer to
`run_in_background`). If the command doesn't finish in time it is **killed and an error is returned**
with whatever output it produced — it is **not** moved to the background. The model sizes `timeout`
to the command; genuinely long-running or must-outlive-the-session work is what the background/detached
modes are for. The one exception is human-initiated: a synchronous command actually runs as a hidden
background process under the hood, so a person can **promote** a still-running one instead of letting
the timeout kill it — in the TUI, `Ctrl+B`; in the Web UI, a button. There's no promote affordance in
IM — only the timeout applies there.

Inside a **sub-agent** even that manual promote is off: every `terminal` call runs synchronously (a
`run_in_background` or `detached` request is ignored), a timed-out command is killed with an error,
and the command isn't promotable (`Ctrl+B` / the button can't target it). A sub-agent returns within
the single turn that spawned it, so it has no later turn in which to collect a backgrounded process's
output — and letting it background one would leak the completion notice into the parent conversation,
unattributed. A genuinely long-running command belongs in the parent, not a sub-agent.

Output is capped at 1MiB of combined stdout+stderr per background process, oldest bytes trimmed
first; there's no cap on how many background processes can run at once, and all tracked ones (not
`detached`) are killed when the host process shuts down.

## Web

| Tool | Purpose |
|---|---|
| `web_fetch` | fetch and read a URL |
| `web_search` | search the web |
| `browser` | drive a real Chrome tab over CDP — see [Browser automation](/docs/guides/browser-automation/) |

## Agents & orchestration

| Tool | Purpose |
|---|---|
| `sub_agent` | spawn a sub-agent (sync or async) |
| `sub_agent_send` / `sub_agent_status` / `sub_agent_kill` | follow up with, poll, or stop an async sub-agent |
| `workflow` | run a deterministic multi-agent orchestration script |
| `workflow_status` / `workflow_kill` | check on or stop a background workflow run (completion is pushed automatically — no polling) |
| `workflow_save` | persist a script as a named, reusable workflow |
| `task_create` / `task_update` / `task_list` | track discrete steps of a larger piece of work |
| `schedule_wakeup` | ask to be resumed after a delay (used by [`/loop`](/docs/guides/loop/)-style recurring work) |

## Goals

| Tool | Purpose |
|---|---|
| `get_goal` / `create_goal` / `update_goal` | read, start, or revise the session's standing objective — see [Goals](/docs/guides/goals/) |

## Skills & MCP

| Tool | Purpose |
|---|---|
| `skill` | load one skill's full instructions on demand |
| `mcp_describe` / `mcp_call` | Tool Search bridge for deferred MCP schemas — see [Connect MCP servers](/docs/guides/connect-mcp-servers/) |

Every connected MCP server's own tools also appear directly as `mcp__<server>__<tool>` when Tool
Search is off (or hasn't activated).

## Interaction & misc

| Tool | Purpose |
|---|---|
| `ask_user_question` | ask the user a clarifying question mid-turn |
| `send_message` | proactively push text to an IM chat that is **not** the current conversation (a normal reply already covers the current one) |
| `send_file` | send a local file over IM — defaults to the current chat; pass `platform`+`chat_id` to target a different one |
| `show_artifact` | display a built HTML/Markdown/image file in the Web UI's artifact panel |
| `restart_server` | request a server [restart](/docs/guides/self-host/#restarting) (e.g. after a config change); always `ask`-class, never allow-listable. Not available in the desktop build, where the server runs in-process with no supervisor — channel config is applied via hot-reload instead. |

Next: see how tool calls are gated in [The agent loop](/docs/concepts/agent-loop/).
