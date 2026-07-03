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
| `workflow_status` / `workflow_kill` | poll or stop a background workflow run |
| `workflow_save` | persist a script as a named, reusable workflow |
| `task_create` / `task_update` / `task_list` | track discrete steps of a larger piece of work |
| `schedule_wakeup` | ask to be resumed after a delay (used by `/loop`-style recurring work) |

## Goals

| Tool | Purpose |
|---|---|
| `get_goal` / `create_goal` / `update_goal` | read, start, or revise the session's standing objective — see [Goals](/docs/guides/goals/) |

## Skills & MCP

| Tool | Purpose |
|---|---|
| `skill` | load one skill's full instructions on demand |
| `mcp_search` / `mcp_describe` / `mcp_call` | Tool Search bridge for deferred MCP schemas — see [Connect MCP servers](/docs/guides/connect-mcp-servers/) |

Every connected MCP server's own tools also appear directly as `mcp__<server>__<tool>` when Tool
Search is off (or hasn't activated).

## Interaction & misc

| Tool | Purpose |
|---|---|
| `ask_user_question` | ask the user a clarifying question mid-turn |
| `send_message` | send a message on the current channel without ending the turn |
| `send_file` | send a file back (IM channels) |
| `show_artifact` | display a built HTML/Markdown/image file in the Web UI's artifact panel |
| `restart_server` | restart `octo serve` (e.g. after a config change) |

Next: see how tool calls are gated in [The agent loop](/docs/concepts/agent-loop/).
