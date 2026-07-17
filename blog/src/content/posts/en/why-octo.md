---
title: "Why I Built Another Open-Source Agent"
description: "The origin story of Octo, a self-hosted AI agent in a single Go binary — why I built it, the design decisions behind it, and what it deliberately won't do."
pubDate: 2026-07-17
author: "Roy Lei"
tags: ["announcement", "octo-agent", "story"]
locale: en
originalSlug: why-octo
---

# Why I Built Another Open-Source Agent

Octo is a personal AI agent that ships as one Go binary: 34 built-in tools, 20 default skills, and 7 of 8 planned interfaces live (CLI, web, desktop, IM bridges, VS Code, Obsidian, Go SDK — mobile is in the works). Install is a one-liner with no Node, Python, or Ruby:

```bash
curl -fsSL https://octo-agent.dev/install.sh | sh
octo config
```

## Why I built it

Two reasons.

First, I was genuinely frustrated with the split workflow — Claude Code for coding, OpenClaw for everyday tasks. Two tools, two configs, two experiences, when they're fundamentally the same thing: a conversation with an agent that has tools. That frustration shaped every design decision in Octo.

Second, the first time I used Claude Code in anger, I was struck by how much was going on under the hood — an AI navigating a terminal, reading files, running commands, correcting itself. I wanted to understand how that actually works, and the best way to understand a system is to build one from scratch. With AI helping write the code, that turned out to be feasible for one person.

## One session, no modes

Most agents split Chat, Coding, and Work into separate modes or apps. Octo doesn't. All three are the same thing — a conversation with an agent that has tools — so Octo has exactly one kind of session that does all of it. You never have to stop and decide which mode you're supposed to be in.

The same reasoning removes the "coding agent" vs "general agent" divide. A general-purpose personal assistant should be able to write code and build a slide deck. There is no reason those are two products.

## A few design decisions worth talking about

**MCP without context bloat.** Octo supports stdio, Streamable HTTP, and OAuth MCP servers, but tool schemas load on demand through a built-in Tool Search bridge — the model looks tools up by name instead of carrying every schema on every turn. You can pile on MCP servers without blowing up the context window.

**The agent can't break its own host.** Octo is a compiled binary, so the agent can't modify its own source the way Node/Python agents installed from source can. Config edits are validated on write and a broken config never takes effect — the last good one keeps working. The agent also can't kill the Octo server process, even if you ask it to: the server's own PID is guarded at the tool layer.

**Deletes are reversible by default.** Models occasionally do something destructive. In Octo, `rm`, file overwrites, and programmatic deletes all stage into a local recycle bin first (14-day retention, size-capped), restorable from the CLI or the web UI. If a file is git-tracked and clean, the backup is skipped — git already has it.

**Browser automation attaches, never launches.** The browser tool drives your real, logged-in Chrome over CDP instead of spinning up a headless instance with no cookies. Recordings compile to semantic selector-based YAML skills that self-heal when the frontend changes.

## Privacy

Octo has zero telemetry. Nothing leaves your machine except the model requests you explicitly send, and you can point it at local models — both wire protocols (Anthropic and OpenAI style) and any compatible endpoint work.

## What it doesn't do (yet)

By default there is **no OS-level isolation** — the permission engine gates commands by rules, and the real sandbox (`--sandbox`, Seatbelt on macOS, Landlock + seccomp on Linux) is opt-in and not available on Windows. Mobile clients aren't shipped yet. And a solo-built agent won't beat frontier-lab agents on raw capability — Octo competes on being self-hosted, private, and having everything in one place.

## Standing on the shoulders of giants

Octo wouldn't exist without these projects: Claude Code (Loop and Skill design), Codex (the Goal mechanism), OpenClaw (the personal-assistant form factor), Hermes (agent interaction design), and OpenClacky (whose web UI inspired Octo's).
