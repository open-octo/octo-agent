---
title: System layers
description: A five-layer, one-directional dependency graph.
---

```
cmd/octo/          CLI entry (chat one-shot + TUI, serve, mcp, slash commands)
   ↓
internal/agent/    History, sessions, content blocks, Sender interface,
                   Agent.Turn / TurnStream / Run (tool-calling loop)
   ↓
internal/provider/ Provider interface + concrete implementations
                   ├─ anthropic/   x-api-key, system top-level, content[].text
                   └─ openai/      Bearer auth, system in messages[0]
   ↓
internal/tools/    ToolExecutor implementations — terminal (+ background),
                   file read/write/edit, glob, grep, web fetch/search, skill
internal/skills/   SKILL.md discovery + system-prompt manifest
internal/permission/  allow/deny/ask rule engine gating every tool call
internal/mcp/      MCP client (stdio + HTTP, OAuth)
internal/server/   octo serve — HTTP REST + WebSocket + embedded dashboard
internal/channel/  IM bridge — adapter interface + WeChat iLink / Feishu /
                   DingTalk / WeCom / Discord / Telegram adapters
```

The dependency direction is enforced, not just documented: `provider` never imports `agent`, and
`agent` never imports `provider` — the agent loop is written against the `Sender` interface, and
`internal/app` is the one place that constructs a concrete provider client and hands it to the
agent as that interface.

## The `Sender` interface stack

Each provider implements both a buffered (`Send`) and streaming (`SendStream`) variant. The agent
layer mirrors that with a stack of interfaces, each building on the last:

```
Sender → StreamingSender → ToolSender → ToolStreamingSender
```

Callers type-assert to the highest capability a given provider actually offers, so a
non-streaming or non-tool-calling provider still works — it just gets fewer capabilities, not a
compile error.

## App bootstrap

`internal/app` is the single place that constructs provider clients and adapts them to
`agent.Sender`. Every entry point — `cmd/octo`, `internal/server`, the IM channels — reaches the LLM
through it rather than importing `provider` directly. It also owns the permission gate, the
sub-agent spawner, and MCP + built-in tool unification (`WireTools`), so those three surfaces stay
in lockstep across the CLI, the web server, and every chat adapter instead of drifting.

Next: [Provider protocols](/docs/architecture/provider-protocols/) covers what `anthropic/` and
`openai/` each normalize away from the agent layer.
