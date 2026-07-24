---
title: Sub-agents
description: Fork the conversation or spawn a specialized, independent agent.
---

The `sub_agent` tool spawns a fresh agent — either a copy of the current conversation, or an
independent one with its own persona and toolset. There's no slash command for this; the model
calls the tool directly when a task calls for isolation or a specialized perspective.

## Fork vs. a typed sub-agent

| | Fork (no type given) | Typed (a `subagent_type`) |
|---|---|---|
| History | Copies the parent conversation so far | Starts empty |
| System prompt | Same as the parent — shares its prompt cache, so it's cheap | The parent's prompt plus the preset's persona text |
| Model | Parent's model, unless overridden | Parent's model, unless the preset or the call overrides it |
| Tools | Full parent toolbelt minus `sub_agent` itself | Parent toolbelt filtered by the preset's `tools`/`disallowed_tools` |
| Use it for | Offloading a noisy investigation and keeping only the conclusion | An independent perspective or a specialized role |

Recursion is blocked one level deep: a sub-agent's own toolbelt never includes `sub_agent`, and
calling it anyway is rejected even if something bypassed that filtering.

## Built-in types

| Type | Read-only | Notes |
|---|---|---|
| `explore` | yes | Fast research, with a trimmed system prompt |
| `plan` | yes | Read-only investigation that produces an implementation plan; trimmed system prompt |
| `general` | no | Full toolbelt, for end-to-end delegation |
| `code-review` | yes | Reviews via `git diff` |

`explore` and `plan` are the only "lean" types — they drop the skills manifest and memory
injection from their system prompt, since a quick research pass rarely needs either. Every type
runs on the parent's model (or an explicit `model` override): a sub-agent's findings gate the
parent's next step, and a downgraded scout returns downgraded findings.

## Custom types

Drop a markdown file at `~/.octo/agents/<name>.md` (user-level) or `.octo/agents/<name>.md`
(project-level, wins on a name collision) and it becomes a `subagent_type` the model can request by
that filename:

```markdown
---
description: Audits code for security vulnerabilities
read_only: true
tools: [read_file, grep, glob, terminal]
disallowed_tools: [write_file, edit_file]
model: inherit
---
You are a security-focused sub-agent. Review the diff for OWASP top 10 issues, hard-coded
secrets, and injection risks. Report file:line findings with severity — don't modify anything.
```

| Field | Required | Meaning |
|---|---|---|
| `description` | yes | Shown to the model so it knows when to reach for this type |
| `read_only` | no | Confines it to non-mutating tools |
| `tools` | no | Explicit allowlist |
| `disallowed_tools` | no | Subtracted from the inherited toolbelt |
| `model` | no | `inherit` (default) or an explicit model id |

The frontmatter `name` field, if present, is ignored — the filename (minus `.md`) is what the model
uses to request this type. Directories are rescanned on every lookup, so an edit takes effect
immediately, no restart needed.

## Following up on an async sub-agent

Spawns can run synchronously or asynchronously. When the model issues several `sub_agent` calls in
one round, they fan out **concurrently** (capped at 16 in flight) instead of running one after
another. In the TUI, `Ctrl+B` promotes the sub-agent you're currently watching to the background
so the main conversation continues without it. An async one that finishes without being killed
stays addressable:

| Tool | Purpose |
|---|---|
| `sub_agent_send` | send a follow-up message to a running or finished async sub-agent |
| `sub_agent_status` | check progress without blocking |
| `sub_agent_kill` | stop one early |

## What a sub-agent can't do

A sub-agent's `terminal` calls always run **synchronously**: a `run_in_background` or `detached`
request is ignored, and a command that exceeds its timeout (120s by default, or the call's explicit
`timeout`) is killed with an error rather than promoted to a background process. A sub-agent returns within the turn that spawned it, so it has
no later turn in which to collect a backgrounded command's output — and a stray background process
would otherwise fire its completion notice into the parent conversation. Hand a genuinely
long-running (or must-outlive-the-agent) command to the parent instead. A sub-agent also can't spawn
its own sub-agent.

Next: orchestrating a whole fleet of sub-agents deterministically, instead of one at a time, is what
[Workflows](/docs/guides/workflows/) are for.
