# Memory System

Octo maintains **long-term memory** across sessions.

## Storage Location

```
~/.octo/memories/
```

Memories are stored as Markdown files with YAML frontmatter.

## How It Works

- The agent automatically reads relevant memories at the start of a session
- New information can be persisted to memory during a session
- Memories are topic-organized and searchable

## Manual Memory Management

Persist and recall are sub-agent presets, not user-callable slash commands.
Ask the agent in natural language and it routes through the `agent` tool
with the right `subagent_type`:

- **Persist** — "remember that we use Railway for staging" → agent forks a
  `persist-memory` sub-agent that writes the file
- **Recall** — "what do you know about our deployment setup?" → agent forks
  a `recall-memory` sub-agent that scans existing memory and summarizes

A `--no-memory` server flag disables automatic persistence (see below).

## Disabling Memory

When starting the web server:

```bash
octo server --no-memory
```

## Memory Files

Each memory file contains:

```yaml
---
topics:
  - topic-name
created_at: 2026-01-01
type: factual
---

Content here...
```

## Scope

- **User-level**: `~/.octo/memories/` — Shared across all projects
- **Project-level**: `.octo/memories/` — Project-specific

The agent loads both scopes and merges them by relevance.
