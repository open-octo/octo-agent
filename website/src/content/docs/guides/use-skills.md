---
title: Use skills
description: Reusable, on-demand instruction sets in Claude Code's SKILL.md format.
---

Skills are reusable instruction sets the model loads only when a task matches — they don't cost
context on turns that don't need them.

## Where skills live

- `~/.octo/skills/<name>/SKILL.md` — user-level, available across all projects.
- `.octo/skills/<name>/SKILL.md` — project-level, takes precedence over user-level.

The format is identical to Claude Code's, so you can symlink `~/.claude/skills` to `~/.octo/skills`
and reuse everything you already have:

```bash
ln -s ~/.claude/skills ~/.octo/skills
```

## Anatomy of a skill

Each `SKILL.md` is YAML frontmatter plus a markdown body:

```markdown
---
name: review
description: Review the current diff for correctness and style
---
Walk the diff hunk by hunk and flag correctness bugs first, then style.
```

At session start, octo lists each skill's name and description in the system prompt — a one-line
manifest, not the full body. The model loads a skill's full instructions on demand (via the `skill`
tool) only when a task matches.

## Using skills

```bash
octo skills list     # see what's discovered
```

In the TUI: `/skills` lists them, and `/<name>` (e.g. `/review`) runs one directly.

Next: skills often reach for MCP tools — see [Connect MCP servers](/docs/guides/connect-mcp-servers/).
