# Skills — defaults, installing, and writing your own

Skills are reusable instruction sets in Claude Code's SKILL.md format (YAML frontmatter `name` + `description`, plus a markdown body) that the model loads only when a task matches — they don't cost context on turns that don't need them.

## Where skills live

```
~/.octo/skills-default/   built-in set, materialized from the binary (octo skills update re-syncs)
   ↓ overridden by
~/.octo/skills/<name>/SKILL.md      user-level, all projects
   ↓ overridden by
.octo/skills/<name>/SKILL.md        project-level, committed with the repo
```

Same format as Claude Code's — `~/.claude/skills` can be symlinked to `~/.octo/skills` to reuse skills you already have.

```
octo skills list      # all skills, grouped default → user → project
octo skills add       # install a skill from a source (guided)
octo skills update    # re-sync the built-in defaults after an upgrade
octo skills path      # print the three roots
```

In the TUI, `/skills` lists them and `/<name>` runs one directly. **IM channels don't support `/<skill-name>` triggering** — ask in plain language there instead.

## Writing your own skill

```markdown
---
name: review
description: Review the current diff for correctness and style
---
Walk the diff hunk by hunk and flag correctness bugs first, then style.
```

`name` and `description` are the only required frontmatter fields — the description is what the model sees to decide when the skill applies, so make it specific about triggers. Drop it under `~/.octo/skills/<name>/` or `.octo/skills/<name>/`. The `skill-creator` default skill can also build one interactively.

Full built-in skill catalog (18 skills, grouped by purpose) and the `octo skills add` source format: **https://octo-agent.dev/docs/guides/use-skills/** (`web_fetch`).
