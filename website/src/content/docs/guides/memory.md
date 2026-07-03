---
title: Give it memory
description: Cross-session memory as plain markdown files, stored locally.
---

octo remembers your preferences, project conventions, and past corrections across sessions —
stored locally in `~/.octo/memories/<repo-slug>/`, never in the cloud.

## How it works

There's no dedicated `remember`/`forget` tool and no code-driven consolidation step. The agent
manages memory the same way it manages any file — with `read_file` / `write_file` / `edit_file` —
keeping to one convention:

- `MEMORY.md` — the index. Loaded into the system prompt every session (first 200 lines / 25KB,
  whichever comes first — mirrors Claude Code's own injection cap).
- `<topic>.md` — detail files the agent creates and reads on demand, linked from the index.

The memory directory is whitelisted for writes, so the agent manages it without permission prompts.

```bash
octo memory list     # list the project's and inherited memory files
octo memory path     # print the project's and inherited memory directories
octo --no-memory     # disable memory injection for a single session
```

## Scope

Memory is scoped per repository, keyed off the git **common** directory rather than the per-worktree
top-level — so every linked worktree of a repo shares one memory scope instead of starting from
empty. Working outside a git repo scopes memory to that directory directly.

A second, home-level index (`~/.octo/memories/<home-slug>/MEMORY.md`) is inherited into *every*
project — the place for things that aren't about one repo, like how you like to work.

## What ends up in it

In practice the agent uses this to track things that aren't recoverable from the code itself:
who's doing what and why, standing preferences you've stated ("always use worktrees for this
repo"), and corrections you've given more than once. It does not duplicate what `git log` or the
code already says.

:::tip
If `MEMORY.md` grows past the injection budget, octo appends a truncation warning rather than
silently dropping content — a signal that it's time to consolidate detail into topic files.
:::

Next: memory pairs well with [hooks](/docs/guides/hooks/) if you want to trigger side effects
(like a save nudge) on specific tool calls.
