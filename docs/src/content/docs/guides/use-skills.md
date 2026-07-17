---
title: Use skills
description: Reusable, on-demand instruction sets in Claude Code's SKILL.md format.
---

Skills are reusable instruction sets the model loads only when a task matches — they don't cost
context on turns that don't need them.

## Where skills live

- `~/.octo/skills-default/` — the built-in set below, materialized from the binary on first run
  (`octo skills update` re-syncs it after an upgrade). Kept in its own directory so refreshing
  defaults never touches a skill you wrote yourself.
- `~/.octo/skills/<name>/SKILL.md` — user-level, available across all projects. A same-named skill
  here overrides a default.
- `.octo/skills/<name>/SKILL.md` — project-level, overrides both of the above on a name collision.

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
octo skills path     # print the user/project/default skill directories
octo skills add      # install a skill from a source (guided)
octo skills update   # re-sync the built-in defaults after an upgrade
```

In the TUI, `/skills` lists them and `/<name>` (e.g. `/review`) runs one directly. In the Web UI,
the composer's `/` menu does the same. **IM channels don't support `/<skill-name>` triggering** —
any `/text` there is matched against the fixed [slash command](/docs/reference/slash-commands/)
set, and an unrecognized one returns "Unknown command" rather than falling through to a skill; ask
for the task in plain language instead and the model loads the matching skill itself.

## Built-in skills

octo ships 20 skills out of the box. Every one triggers automatically when the model judges a task
matches its description — you rarely need to invoke them by name.

**Get started**

| Skill | What it does |
|---|---|
| `onboard` | First-run setup (name, personality, profile → `soul.md` + `user.md`); also handles narrower re-curation with `scope:soul`, `scope:user`, or a specific memory file path |
| `product-help` | Answers "how do I…" / "what is…" questions about octo itself by reading its own product docs |
| `skill-creator` | Turns a repeatable task into a new `SKILL.md`, or edits/improves an existing one |
| `workflow-creator` | Chains **existing** skills and browser recordings, or composes fresh `agent`/`parallel`/`pipeline` orchestration, into one runnable, saved [workflow](/docs/guides/workflows/) |

**Build & ship code**

| Skill | What it does |
|---|---|
| `tech-design` | Produces a full backend technical design doc from a PRD or feature description |
| `grill-me` | Interviews you about a plan until every open decision is resolved — pairs with `tech-design` |
| `implement` | Decomposes a tech design into dependency-ordered slices, TDD-executes each, reviews each with a sub-agent, and checkpoints progress so it survives a restart |
| `code-review` | Reviews the current diff with an isolated sub-agent for unbiased correctness/convention/security feedback |
| `worktree-isolate` | Does a risky or experimental change in an isolated git worktree, then merge or discard |

**Automation & scheduling**

| Skill | What it does |
|---|---|
| `cron-task-creator` | Creates/inspects/edits/deletes recurring prompts that survive a restart, run by `octo serve`'s scheduler. See [Schedule cron tasks](/docs/guides/cron-tasks/) |
| `loop-engineering` | Designs a self-running agent loop from octo's built-in primitives — discovery, isolated workers, verification, persisted state — with an L1→L3 rollout plan |

**Connect things**

| Skill | What it does |
|---|---|
| `mcp-creator` | Finds the right MCP server, writes the `mcp.json` entry, and verifies the connection |
| `channel-manager` | Walks you through setting up an IM platform (Feishu, WeChat, WeCom, DingTalk, Discord, Telegram) and writes `channels.yml` |

**Documents, media & research**

| Skill | What it does |
|---|---|
| `web-access` | Methodology + a cross-session experience library for hard web targets: login-gated or anti-bot sites, unknown page structure, cross-source verification |
| `artifact-design` | Design guidance for any self-contained HTML/Markdown page shown in the Artifacts panel — reports, dashboards, diagrams, generated UIs |
| `dataviz` | Chart/graph/dashboard rules — chart-type selection, categorical/sequential/diverging color systems, legend/axis/tooltip conventions |
| `office-xlsx` | Creates/reads/edits `.xlsx` spreadsheets — formulas, styling, merged cells, multiple sheets, charts, validation |
| `ppt-master` | Turns a document (PDF/DOCX/URL/Markdown) into an editable PowerPoint deck — SVG-authored slides with native charts/tables and speaker notes, exported to `.pptx` |
| `image-gen` | Generates images with an AI model (14 provider backends) or sources openly-licensed stock, saved to files — one-off or batch; other skills like `ppt-master` delegate to it |
| `deep-research` | Deep, multi-source, fact-checked research — fans out searches, reads primary sources, adversarially verifies claims, and synthesizes a cited report |

Next: chaining several of these into one saved flow is exactly what [Workflows](/docs/guides/workflows/) are for.
