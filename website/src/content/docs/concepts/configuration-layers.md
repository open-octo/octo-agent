---
title: Configuration layers
description: How octo composes its system prompt from identity, profile, and rule files.
---

octo composes its system prompt from several optional layers — later overrides/extends earlier:

| Layer | Scope | Purpose |
|---|---|---|
| `~/.octo/soul.md` | global | agent identity & behavior, an openclaw/hermes-style persona |
| `~/.octo/user.md` | global | who you are — a profile injected into every session |
| `~/.octo/octorules.md` | global | your cross-project rules and preferences |
| `.octorules` | per-repo | project conventions, committed with the repo |
| `--system "..."` | one-off | override for a single run |

Generate a starting `.octorules` for the current repo with `octo init` (or `/init` in the TUI) —
it inspects the codebase and drafts conventions rather than leaving you with a blank file.

## `@include`

Identity and rule files support `@include path/to/fragment.md` to pull in shared content — useful
for a fragment reused across several `.octorules` files in related repos.

Next: the memory system layers on top of this as a separate, per-repo index — see
[Give it memory](/docs/guides/memory/).
