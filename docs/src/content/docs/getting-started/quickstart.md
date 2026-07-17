---
title: Quickstart
description: From install to a finished task in under five minutes.
---

```bash
export ANTHROPIC_API_KEY=sk-ant-...      # or OPENAI_API_KEY=...

# One-time setup: save your default provider/model (skip the export above next time)
octo config
```

## Headless one-shot

`octo` in a script or CI is a `claude -p`-style one-shot: one prompt, a full agentic tool loop, then
exit. Built-in tools (shell, read/edit files, search), MCP servers, and skills are all on by default,
so a single message can actually do work.

```bash
octo "Add a --json flag to 'octo config show' and run the tests"

# The prompt can also come from a pipe or a file — handy for scripts / CI:
echo "Summarise what changed in the last commit" | octo
octo --prompt-file ./task.md
```

## Interactive multi-turn

Run `octo` in a terminal with no message to get the TUI — rich tool cards, session auto-saved.

```bash
octo
octo sessions        # list saved sessions
octo -c              # pick a recent session from a list
octo -c <session-id>
```

## Streaming and reasoning

Streaming is on by default; `--stream=false` buffers and prints only the final reply text — clean
for capturing into a file.

```bash
octo --stream=false "..."

# Extended reasoning: set the intensity. The terminal never renders the
# thinking trace; --show-reasoning only controls whether the Web UI gets it.
octo --reasoning-effort high "..."
octo --show-reasoning=false "..."   # keep reasoning enabled but hide the trace from the Web UI
```

## Plain chat, no tools

```bash
octo --no-tools "..."
```

## Repo conventions

```bash
octo init            # generate a .octorules guide for this repo
```

Next: [Choose a provider](/docs/getting-started/choose-a-provider/), or jump straight into a
[Guide](/docs/guides/connect-mcp-servers/).
