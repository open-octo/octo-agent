---
name: loop-engineering
description: Design a self-running coding-agent loop using octo-agent's built-in primitives. Use when the user wants "loop engineering", "design a loop", "build an agent loop", "写一个循环", "设计一个自动循环" — or when they want a recurring task that discovers work, hands it to isolated agents, verifies results, and persists state. Trailing text is the loop they want to build.
---

# Loop Engineering with octo-agent

Design a **self-running loop** that replaces you as the person prompting the agent.
octo-agent already ships every primitive you need; this skill is the glue that
orchestrates them.

## What a loop looks like

A loop is a recursive goal that keeps finding work, acting on it, checking the
result, and remembering what happened. The canonical shape is:

```text
Trigger (cron / schedule_wakeup / event)
  → Triage / Discovery
  → Read + Write STATE / Memory
  → Isolated Worktree
  → Implementer Sub-agent
  → Verifier Sub-agent
  → MCP / Git / Ticket connectors
  → Human Gate (merge, deploy, close)
  → Loop again
```

## octo-agent primitives for each block

| Loop block | octo-agent primitive | How to invoke |
|---|---|---|
| **Trigger** | `/loop` in-session; `cron-task-creator` / `/api/tasks` for persistent schedules | `/loop 1h check CI` or create a task with `cron` |
| **Goal** | `/goal <objective>` or `create_goal` tool | `/goal all open issues older than 7d have a response` |
| **Triage** | A sub-agent or `workflow` | `sub_agent` with `read_only` true; or `workflow` tool |
| **State** | `STATE.md` / `LOOP.md` + `MEMORY.md` | `write_file` to `.octo/STATE.md` |
| **Worktree isolation** | `worktree-isolate` skill or `workflow` `isolation: "worktree"` | call `skill` or pass `isolation` to sub-agent/workflow |
| **Implementer** | `sub_agent` tool or `agent()` in workflow | spawn with self-contained prompt |
| **Verifier** | `code-review` skill or a second `sub_agent` | independent checker, no context from implementer |
| **Connectors** | `mcp` tool + configured MCP servers | query issue tracker, Slack, staging API |
| **Human Gate** | Final report only; never auto-merge/deploy/close unless explicitly allowed | stop and ask user |

## Design process

### 1. Scope the loop

Before writing anything, define:

- **Input**: what triggers it? (cron, event, manual `/loop`)
- **Discovery**: what does it look at? (issues, CI, commits, alerts, diffs)
- **Done condition**: when does one iteration stop? ("all items triaged", "patch verified", "nothing new")
- **Safety**: what must NEVER happen unattended? (merge, deploy, delete branch, close issue)
- **Output**: what does it leave behind? (STATE.md, PR, report, ticket update)

If any of these are unclear, stop and ask the user — don't design a loop that can
auto-merge because the user forgot to mention a gate.

### 2. Pick the right persistence model

| Cadence | Use |
|---|---|
| One-shot, in-session | `schedule_wakeup` with `/loop` |
| Recurring while octo is running | `schedule_wakeup` with `repeat: true` |
| Survives restart, fires while away | `cron-task-creator` / `POST /api/tasks` |
| Multi-step, parallel agents | `workflow` tool |

For a production-grade loop, prefer **cron-task-creator** so the loop keeps running
after you close the session.

### 3. Create the state file

Every loop must write state to disk. Use a file the loop can read next time:

```markdown
# .octo/STATE.md

## Loop: <name>
## Owner: <user>
## Last run: <RFC3339>

### Done
- [x] item 1

### In Progress
- [ ] item 2

### Open
- [ ] item 3

### Notes
- tried X, failed because Y
```

Update this file at the end of every run. The next run reads it first.

### 4. Split maker and checker

Never let the same agent grade its own work. A loop must have:

- **Implementer**: produces the change/fix/draft
- **Verifier**: checks it against a written standard, tests, or project skills

Use `sub_agent` with no shared context, or use the `code-review` skill for code changes.

### 5. Run in a worktree when touching files

If the loop writes code, use worktree isolation so a failed run doesn't corrupt
the main checkout. Two ways:

1. Call `skill` for `worktree-isolate` and pass the task as trailing text.
2. Use the `workflow` tool with `isolation: "worktree"` (see `daily-triage` example).

### 6. Human gate for irreversible actions

A loop may:
- Draft PRs
- Write reports
- Update STATE.md
- Suggest labels / assignments

A loop must NOT, without explicit user approval:
- Merge a PR
- Deploy
- Close an issue
- Delete a branch or tag
- Send a message in a public channel

When a loop wants to do one of these, stop and ask.

### 7. Start with L1 (report-only)

Roll out in phases:

- **L1**: read-only triage, report to user, no auto-fix
- **L2**: draft fixes in worktree, ask before applying
- **L3**: unattended fixes, but only for safe, well-defined cases

Never jump to L3 on the first run.

## Output of this skill

After using this skill, you should produce:

1. A `LOOP.md` in the project root describing the loop's purpose, cadence, and safety rules.
2. A `STATE.md` template in `.octo/` for the loop to use across runs.
3. Optionally a `workflow` template or a scheduled task definition.
4. A one-run validation: execute the loop once and report what it found and what it did.

## Built-in workflow

octo-agent embeds one workflow that demonstrates the full Loop Engineering
pattern end-to-end: discover → triage → worktree fix → verify → state → report.
Invoke it directly with the `workflow` tool by name:

```bash
octo workflow daily-triage '{"repo": ".", "since": "1d"}'
```

| Template | Purpose | Risk level | Typical cadence |
|---|---|---|---|
| `daily-triage` | Discover open issues, recent CI failures, and commits; draft fixes in worktrees; verify with a second agent. | Medium | Daily |

## Reference templates (this skill's `templates/` directory)

This skill also ships a handful of Loop Engineering examples covering other
common maintenance loops. They are **not** registered as embedded workflows —
running `octo workflow issue-triage ...` will not find them. Instead, read the
one that fits, adapt it (repo path, args, prompts) to the user's actual
situation, then either pass it inline as `workflow(script: ...)` or persist it
with `workflow_save` if the user wants to reuse it. Treating them as read-and-adapt
starting points rather than one-size-fits-all defaults matches the design-process
rule above: scope, safety, and output are specific to each user's repo, and a
template that's silently wrong for their case is worse than no template.

| Template | Purpose | Risk level | Typical cadence |
|---|---|---|---|
| `issue-triage` | Categorize open issues, suggest labels, identify missing info, and route to owners. | Low | Daily / on new issue |
| `pr-babysitter` | Watch open PRs, flag stale ones, detect merge conflicts, and suggest next actions. | Low | Daily |
| `ci-sweeper` | Monitor CI failures, classify root causes, retry flaky jobs, and draft fixes for real failures. | **High** | On CI failure / hourly |
| `dependency-sweeper` | Update patch/minor dependencies in a worktree and run tests. | Medium | Weekly |
| `changelog-drafter` | Draft a changelog from commits since the last tag. | Low | Before release |
| `post-merge-cleanup` | Delete merged branches and plan linked ticket updates. | Low (destructive: branches) | After merge |

All destructive templates default to dry-run unless the user explicitly passes
`apply: true` or `dry_run: false`.

## Template: LOOP.md

```markdown
# Loop: <name>

## Purpose
<one sentence>

## Trigger
<cron expression or event>

## Discovery
<what the loop looks at>

## Done condition
<when does one iteration stop>

## Safety
<what the loop must NOT do unattended>

## State file
`.octo/STATE.md`

## Rollout
- L1: report only
- L2: draft fixes, ask before applying
- L3: unattended fixes (only after L2 is stable)
```

## Risks to mention

- Token costs can explode with sub-agents and frequent runs.
- Verification is still the user's responsibility.
- Comprehension debt grows when the loop ships code faster than the user reads it.
- Two people can run the same loop and get opposite results — the loop doesn't know the difference.

> Build the loop, but build it like someone who intends to stay the engineer,
> not just the person who presses go.
