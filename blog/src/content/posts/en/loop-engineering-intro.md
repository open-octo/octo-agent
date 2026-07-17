---
title: "Loop Engineering Series (1): From Prompt to System"
description: "When AI stops answering one-off questions and starts running a workflow continuously, observably, and reversibly — that's Loop Engineering."
pubDate: 2026-07-03
author: "octo-agent team"
tags: ["loop-engineering", "ai-agent", "octo-agent", "workflow"]
locale: en
originalSlug: loop-engineering-intro
---

# Loop Engineering Series (1): From Prompt to System

> When AI stops answering one-off questions and starts running a workflow continuously, observably, and reversibly — that's Loop Engineering. This is the first post in the octo-agent official technical series.

---

## What is octo-agent

octo-agent is a **local AI agent platform for engineering practice**. It is not meant to be a smarter chatbot; its goal is to let AI execute repetitive engineering tasks in a stable, observable, and reversible way.

Unlike general-purpose ChatGPT clients or one-shot code-generation tools, octo-agent emphasizes:

- **Local-first**: it runs on your machine, with code and state kept in the local repository.
- **Workflow-driven**: complex tasks are orchestrated via `workflow` scripts, not left to the luck of a single prompt.
- **Repeatable**: the same workflow can be run again and again, with predictable behavior.
- **Observable**: `.octo/` holds all loop state, logs, and execution history in one place.
- **Progressive**: through L1 / L2 / L3 phases, automation graduates from "read-only" to "act".

It also provides a `skill` system for capturing best practices, MCP for connecting external tools, and `sub_agent` for multi-agent collaboration. Loop Engineering is a working pattern built on top of these capabilities.

---

## What is Loop Engineering

The traditional way: you type → AI replies → you check → you type again. Every task requires you to initiate, follow up, and close it manually.

Loop Engineering is different: you design a system that discovers tasks, delegates them to sub-agents, verifies results, records state, runs on a schedule, and only calls a human at key decision points.

Google engineer **Addy Osmani** captured it best:

> “Loop engineering is replacing yourself as the person who prompts the agent. You design the system that does it instead.”

Anthropic Claude Code lead **Boris Cherny** put it closer to engineering reality:

> “I don’t prompt Claude anymore. I have loops running that prompt Claude and figuring out what to do. My job is to write loops.”

Put differently: what you deliver is no longer a prompt — it's a **stateful, feedback-driven, reversible** working system.

---

## Why Loop Engineering is becoming mainstream

Part of it is tooling maturity: modern coding agents have made the primitives a loop needs — scheduling, isolation, skills, connectors, sub-agents, memory — first-class citizens, so building a loop no longer means gluing scripts together yourself. The other part is that the people building the tools changed how they work first: "I don't write prompts; I write loops" comes from the Claude Code lead, not from marketing copy.

Zoom out and this is the natural next step in the prompt engineering → context engineering → harness engineering progression: AI applications are moving from single interactions to continuously running systems. For an engineer the payoff is twofold — design once and it runs repeatedly (CI failures, issue triage, dependency updates, and other maintenance chores fit especially well), and the human is freed from initiating and following up, keeping only the parts that genuinely need judgment.

---

## The six building blocks, and what they look like in octo-agent

For a loop to run itself, it has to answer five questions: when do I wake up (scheduling), where do I work without stepping on anyone (isolation), what rules do I follow (project knowledge), how do I talk to the outside world (connectors), and who checks my work (division of labor) — plus a sixth: how do I avoid amnesia between rounds (memory/state). The industry has settled on these six primitives, and octo-agent ships all of them built in:

| Module | Problem it solves | Implementation in octo-agent |
|--------|---------|------------------------|
| **Scheduling / Automations** | when to wake, discovering tasks | cross-session: `cron-task-creator` skill, `/api/tasks`; in-session: `/loop` + `schedule_wakeup` |
| **Worktrees** | parallel agents not stepping on each other's files | `worktree-isolate` skill, workflow's `isolation: "worktree"` |
| **Skills** | codifying project knowledge into reusable instructions | `SKILL.md` + the `skill` tool |
| **Connectors (MCP)** | reaching issue trackers, Slack, external APIs | `mcp` tool + MCP servers |
| **Sub-agents** | separating maker and checker | `sub_agent` tool, `agent()` in workflows |
| **Memory / State** | persisting across conversations | `MEMORY.md`, `.octo/STATE.md`, task state |

On top of these six, octo adds two things that tie them together: `workflow` (multi-agent orchestration — phases, parallelism, pipelines) and `/goal` (set an objective, run until the condition is met). `workflow` orchestrates, cron schedules, worktrees isolate, `sub_agent` divides labor, `skill` encodes practice, and `MEMORY.md` / `.octo/` hold state — assembled together they form a Loop Engineering platform rather than a pile of loose tools.

---

## L1 / L2 / L3: three gears of progressive automation

Loop Engineering is not a binary “manual or fully automatic” choice. octo-agent splits it into three gears:

| Gear | Behavior | Use when | Risk |
|------|----------|----------|------|
| **L1: Read-only report** | Discovers, classifies, generates reports; makes no external changes | First 1–3 runs of a new loop | Very low |
| **L2: Draft assistant** | Proposes fixes, draft comments, PR descriptions; waits for human confirmation before publishing | Loop classification quality is stable | Low |
| **L3: Auto-execute** | Executes safe actions automatically (labels, reminders, worktrees, draft PRs); irreversible actions still require human gate | Loop is validated and safety boundaries are clear | Controllable |

The value of this progressive model is that **automation starts from “observation” and gradually earns “action”**. This is the key to preventing loops from running out of control.

---

## How to implement a loop in octo-agent

### Step 1: Write a LOOP.md

Before writing any loop, write a `LOOP.md` that locks down purpose, trigger, scope, done condition, safety boundaries, and rollout plan:

```markdown
# Loop: dependency-sweeper

## Purpose
Automatically upgrade patch/minor dependencies every week.

## Trigger
Every Monday at 9 AM (cron: `0 9 * * 1`).

## Discovery
Run `go list -u -m all` / `npm outdated` / `pip list --outdated`.

## Done condition
All safe patch/minor dependencies are upgraded and tested; major dependencies are listed for human handling.

## Safety
- Only upgrade patch/minor; never touch major.
- Run tests in an isolated worktree; do not pollute main.
- Do not auto-merge / deploy / close issues.

## State file
`.octo/dependency-sweeper-state.md`
```

### Step 2: Choose a trigger

| Scenario | Recommended way | Example |
|----------|-----------------|---------|
| One-shot, repeated within a session | `schedule_wakeup` (`/loop` skill) | `octo /loop 30m check for unmerged PRs` |
| Long-running, across sessions | `cron-task-creator` | create a `cron` task with a `prompt` and `directory` |
| Complex multi-agent flow | `workflow` tool | `octo workflow daily-triage '{"since": "1d"}'` |

### Step 3: Use the built-in `loop-engineering` skill

```bash
octo /loop-engineering design a daily triage loop for my Go backend
```

This skill will:
1. Map out the full chain: trigger → discovery → state → worktree → implementer → verifier → human gate.
2. Generate `LOOP.md` and `STATE.md` templates.
3. Remind you to choose an L1 → L2 → L3 rollout strategy.

### Step 4: Orchestrate with `workflow`

`workflow` scripts run in an **IO-free mruby sandbox**: the script itself cannot touch the filesystem or network; all real IO must be delegated to a child agent via `agent()`:

```ruby
# ✅ Correct: let a child agent write the file
agent("Write a report to .octo/STATE.md with this content: ...", { "read_only" => false })

# ❌ Wrong: File.write inside the script
File.write(".octo/STATE.md", "...")
```

This design forces every loop step through LLM reasoning, while keeping the script itself safe.

---

## A typical loop lifecycle

```text
Scheduled trigger (cron or /loop)
  → Discover tasks (issues / PRs / CI / commits)
  → Classify and prioritize
  → Write to STATE.md / update state
  → For low-risk tasks: spin up an implementer sub-agent in an isolated worktree
  → verifier sub-agent independently checks patch + tests
  → Within safety / allowlist: auto-open PR / update ticket / add label
  → Risky / ambiguous: escalate to human gate
  → Next run resumes from STATE.md
```

This is a **recursive goal**: define the objective, and AI iterates until it is complete or escalated. `STATE.md` and `processed.json` give the loop memory so it does not lose progress when conversation context resets.

---

## Four safety red lines

The stronger the automation, the more important the safety boundaries. octo-agent recommends these four red lines:

1. **Maker and checker must be separated**: use `sub_agent` or `agent()` to designate implementer and verifier.
2. **File writes must be worktree-isolated**: pass `isolation: "worktree"` to avoid polluting main.
3. **Irreversible actions must have a human gate**: merge, deploy, close issues, delete tags, post to public channels — loops may propose, but not execute.
4. **Start from L1**: the first version is always report-only; never auto-edit code on day one.

---

## Where Loop Engineering applies

Loop Engineering is not limited to coding. Any task that meets four conditions — **regular inputs, clear judgment criteria, iterative improvement, and irreversible actions that need a human gate** — can use it.

| Role | Tasks to turn into loops | How to run |
|------|--------------------------|------------|
| **Backend engineer** | Dependency upgrades, issue triage, CI failure classification, post-merge cleanup | workflow + cron |
| **Content creator** | Daily trend scan → topic selection → first draft → human review | workflow + scheduler |
| **Freelancer** | Weekly client-email sorting → classification → draft replies | MCP + email + agent |
| **Account manager** | Daily CRM review → flag follow-ups → prepare talking points | connect to Salesforce/HubSpot |
| **Lawyer / legal** | Contract pre-screening: extract key clauses → flag risks → human review | read-only loop, L1 |
| **Personal productivity** | Weekly note/bill/todo consolidation → categorize → generate next-week priorities | local files + memory |

Development has natural advantages: version control, tests, and type systems provide objective validation; `git worktree` and CI provide reversible sandboxes; issues, PRs, and CI produce structured data that agents can read. Non-code scenarios are usually better suited to L1 reports and L2 drafts with human confirmation, with only a few low-risk tasks reaching L3.

---

## Common risks and mitigations

| Risk | Why it happens | Mitigation |
|------|----------------|------------|
| **Token cost explosion** | Sub-agents + long loops + high-frequency scheduling burn tokens fast | Limit batch size, lower frequency, start from L1. |
| **Verification debt** | Nobody reviews the loop’s output | Every run writes to `STATE.md`; review it regularly. |
| **Comprehension debt** | The loop writes code faster than you can understand it | Start with low-risk tasks; keep a human gate. |
| **Cognitive surrender** | Shifting from “using loops to accelerate work I understand” to “letting loops think for me” | Reserve loops for repetitive tasks; keep judgment for humans. |

Addy Osmani’s warning belongs right here:

> “Build the loop. But build it like someone who intends to stay the engineer, not just the person who presses go.”

---

## Where to start

If you are new to Loop Engineering, start with **repetitive maintenance tasks** rather than core business-logic refactoring. Recommended priority:

1. **Daily Triage**: every morning scan issues / PRs / CI and save the “what happened today?” context switch.
2. **Dependency Sweeper**: upgrade patch/minor dependencies weekly, especially in Go where API compatibility is usually reliable.
3. **Post-Merge Cleanup**: auto-delete merged branches and remind people to close linked issues. Low blast radius, high annoyance value.

What Loop Engineering really saves is less the time spent writing code than the context switching and accumulated busywork — leaving human energy for design, architecture, and genuinely complex bugs.

---

## Next in the series

- **Loop Engineering Series (2)**: [Loop Engineering in Practice: Using octo-agent to automate an open-source repository](/blog/posts/en/loop-engineering-practice/) — a full walkthrough of building three loops (issue triage, PR review, auto issue-fix) on the `open-octo/octo-agent` repo, with real flowcharts and lessons learned.

---

## References

- Addy Osmani: https://addyosmani.com/blog/loop-engineering/
- Cobus Greyling reference repo: https://github.com/cobusgreyling/loop-engineering
- The New Stack coverage: https://thenewstack.io/loop-engineering/
- Anthropic harness design: https://www.anthropic.com/engineering/harness-design-long-running-apps
- Anthropic effective harnesses: https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents
- Loop Engineering guide site: https://loopengineering.run/

---

*This is the first post in the octo-agent official technical series.*
