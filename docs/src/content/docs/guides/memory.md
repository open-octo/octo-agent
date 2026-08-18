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

The whole `~/.octo/memories` tree gets an automatic `allow` rule for `write_file`/`edit_file` in the
[permission engine](/docs/reference/permissions/), so the agent manages its notes without a prompt on
every write — including filing a fact into *another* project's memory when you ask it to work on a
repo other than the current directory. Everything outside that tree is still gated normally, and
`--no-memory` withdraws the rule along with the injection.

```bash
octo memory list           # list the project's and inherited memory files
octo memory path           # print the project's and inherited memory directories
octo memory path <dir>     # …for another directory (how the agent resolves another repo's memory)
octo --no-memory           # disable memory injection for a single session
```

## Scope

Memory is scoped per repository, keyed off the git **common** directory rather than the per-worktree
top-level — so every linked worktree of a repo shares one memory scope instead of starting from
empty.

A directory that is **not** a git repo — the default `~/Octo` workspace, `~`, a scratch path — has no
project of its own, so it uses the shared home-level memory below instead of getting a directory of
its own. Such a session is usually working on code somewhere else entirely ("fix the login bug in
project X"), and notes filed under the scratch directory would be invisible to the session that later
works inside project X, while the shared set is read by every session.

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

## When memory changes take effect

Every transport — CLI, web, and IM — composes the system prompt once, the first time a session
takes a turn, and reuses that exact text for the rest of the session's life: resuming it later, or
an IM chat being rebound to it, picks up the same frozen prompt rather than recomposing it. This is
deliberate, not an oversight: recomposing on every turn would vary the text on anything that can
change mid-session — the memory file included — and invalidate the provider's prompt-cache prefix on
that turn and every one after.

So what you tell the agent mid-session is already live in that conversation's own history — it
doesn't need a fresh read of `MEMORY.md` to know it. What the agent *writes* to memory during a
session surfaces starting the *next* session (over any transport), which is exactly when a plain
index file is supposed to matter — for a conversation that hasn't lived through the one where it was
learned.

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
