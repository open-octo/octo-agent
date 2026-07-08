# Skills — defaults, installing, and writing your own

Skills are reusable instruction sets in Claude Code's SKILL.md format: YAML frontmatter (`name` + `description`) plus a markdown body. At session start octo lists each discovered skill's name and description in the system prompt; the model loads a skill's full instructions on demand (via the `skill` tool) when a task matches, or a user triggers one explicitly with `/<name>` in the TUI.

## 1. Where skills come from

```
default  ~/.octo/skills-default/   (shipped, materialized from the binary)
   ↓ overridden by
user     ~/.octo/skills/           (yours, or `octo skills add`)
   ↓ overridden by
project  ./.octo/skills/           (committed with the repo)
```

`Discover` scans lowest-first and `scanRoot` overwrites by name, so a user- or project-level skill with the same name as a default silently wins. `octo skills list` tags each with its source (default/user/project).

## 2. Default skills (embedded, not downloaded)

A curated set ships **embedded in the binary** via `//go:embed` and is materialized to `~/.octo/skills-default/` on first run of each version — offline-capable (works for `go install`, air-gapped, CI), immune to supply-chain/network failure, and never touches `~/.octo/skills/` (your own skills). A version bump wipes-and-rewrites the default dir; a read-only `$HOME` degrades to "no defaults" rather than erroring.

Today's default set (`internal/skills/defaults/`, 20 skills): `channel-manager`, `code-review`, `cron-task-creator`, `deep-research`, `find-skills`, `grill-me`, `implement`, `loop`, `loop-engineering`, `mcp-creator`, `office-xlsx`, `onboard`, `product_help` (this skill), `skill-creator`, `tdd`, `tech-design`, `web-access`, `workflow-creator`, `worktree-isolate`, `zoom-out`. Keep the set small and high-value — every default costs system-prompt manifest space for every user.

```
octo skills list      # all skills, grouped default → user → project
octo skills update    # force re-materialize the defaults (ignores the version stamp)
octo skills path      # print the three roots
```

## 3. Installing a skill from GitHub

`octo skills add <owner/repo[/sub/path] | github.com tree URL> [--force]` fetches a skill's directory straight into `~/.octo/skills/<name>/`:

```
octo skills add anthropics/skills/skills/docx
octo skills add https://github.com/anthropics/skills/tree/main/skills/pdf
```

Fails if a skill with that name already exists there — pass `--force` to replace it. The skill is copied for local use; check the source repository's license before redistributing it.

## 4. Writing your own skill

Drop a directory under `~/.octo/skills/<name>/` (user-level, all projects) or `./.octo/skills/<name>/` (project-level, committed with the repo) containing a `SKILL.md`:

```markdown
---
name: review
description: Review the current diff for correctness and style
---
Walk the diff hunk by hunk and flag correctness bugs first, then style.
```

`name` and `description` are the only required frontmatter fields — the description is what the model sees in the manifest to decide when the skill applies, so make it specific about triggers ("use when...") rather than just naming the topic. The body is whatever instructions/context the skill needs; it can reference sibling files in the same directory (e.g. templates, reference docs) that the model reads on demand. The format is identical to Claude Code's, so `~/.claude/skills` can be symlinked to `~/.octo/skills` to reuse skills you already have. The `skill-creator` default skill can also build one interactively.

## 5. Adding a *default* skill (contributing to octo itself)

Drop `internal/skills/defaults/<name>/SKILL.md` in the octo-agent repo (standard SKILL.md frontmatter). It ships on the next release and materializes on the user's next run after upgrading.
