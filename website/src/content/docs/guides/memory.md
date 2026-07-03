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

The memory directory gets an automatic `allow` rule for `write_file`/`edit_file` in the
[permission engine](/docs/reference/permissions/), so the agent manages it without a prompt on
every write — everything outside the memory directory is still gated normally.

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
project, injected **before** the project's own index — it's the place for things that aren't about
one repo, like how you like to work; project-specific facts belong in the project's own memory.

## Two rule tiers, beyond plain notes

`MEMORY.md` supports two optional sections that behave differently from a plain pointer index:

- **Always-apply rules** — restated on every single turn, for something that must never be missed.
- **Triggered rules** — each written as a rule plus a set of trigger keywords; recalled once per
  session the first time one of its keywords appears in what you type (English keywords match on
  word boundaries, so `deploy` doesn't fire on `deployment`; Chinese keywords match as a substring).

Both are delivered as a reminder attached to your message rather than edited into the system
prompt, so the cached prompt prefix stays byte-stable. A plain index with neither section costs
nothing extra beyond the index itself.

## The save-nudge

After a `terminal` call whose command matches `gh pr create` or `gh pr merge` succeeds, octo appends
a one-time reminder to that tool's result suggesting the model check whether anything from the just-
landed work is durable enough to record — a settled decision, a ruled-out approach, a constraint
future sessions need to respect. It fires at most once per user turn, so a long streak of git
commands doesn't nag repeatedly; see it listed alongside every other configured hook via
[`octo hooks list`](/docs/guides/hooks/).

## Freshness differs by transport

- **Web and IM** recompose the system prompt fresh on every turn, so anything written to `MEMORY.md`
  — by this session or another — is visible starting the very next turn. IM specifically drops and
  rebuilds this state on `/bind`/`/unbind`, so a rebound chat picks up whatever the session's memory
  currently contains.
- **The CLI composes once**, when the interactive session starts, and reuses that system prompt for
  the rest of the process. What the agent writes to memory during a CLI session surfaces the *next*
  time you run `octo` in that repo, not later in the same run.

## What ends up in it

In practice the agent uses this to track things that aren't recoverable from the code itself:
who's doing what and why, standing preferences you've stated ("always use worktrees for this
repo"), and corrections you've given more than once. It does not duplicate what `git log` or the
code already says.

:::tip
If `MEMORY.md` grows past the injection budget, octo appends a truncation warning rather than
silently dropping content — a signal that it's time to consolidate detail into topic files.
:::

Next: memory pairs well with [hooks](/docs/guides/hooks/) for other side effects beyond the built-in
save-nudge.
