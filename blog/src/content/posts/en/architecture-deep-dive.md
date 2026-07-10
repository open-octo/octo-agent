---
title: "octo-agent Architecture Deep Dive: Five-Layer Core Stack & Full-System Dependency Map"
description: "An interactive panorama architecture diagram parsing octo-agent's five-layer unidirectional dependency from CLI/IM entry to LLM backends, the agent leaf package design, protocol-agnostic abstraction, and cross-cutting concerns like permission, workflow, and memory."
pubDate: 2026-07-08
updatedDate: 2026-07-10
author: "octo-agent team"
tags: ["architecture", "deep-dive", "engineering", "ai-agent"]
locale: en
originalSlug: architecture-deep-dive
---

# octo-agent Architecture Deep Dive: Five-Layer Core Stack & Full-System Dependency Map

> octo-agent's codebase has grown to 480+ Go files, six IM bridges, and an embedded Svelte 5 workbench. This post uses an interactive panorama diagram to show how the whole system hangs together.

---

## Design Philosophy: Strictly Unidirectional Dependencies

The single most important architectural discipline in octo-agent is **strictly unidirectional dependency**. The five-layer stack:

```
cmd/ → app/ → agent/ ← (provider/, tools/)
```

`internal/agent/` is a **leaf package** in the dependency tree — it imports nothing from upper layers (no `provider`, `tools`, `server`, `channel`, or `web`). All protocol details, tool implementations, and UI rendering are "pushed upward."

This means you can:
- Swap LLM backends (anthropic → openai → deepseek) without touching a single line of agent code.
- Add a new tool (terminal, browser, skill, workflow, MCP…) without modifying the agent loop.
- Change the rendering surface (T1 UI, Web, IM, Headless) without affecting core logic.

This one rule is the main reason octo-agent has been able to scale.

---

## The Architecture Panorama

<iframe
  src="/blog/architecture-map.html"
  width="100%"
  height="1400"
  style="border: none; border-radius: 8px;"
  loading="lazy"
</iframe>

> 👆 Click any card's title to expand and see key file paths and design details.

---

## Seven Facets at a Glance

### 1. Entry Layer: One Agentic Loop, Many Faces

TUI (Bubble Tea), Web (Svelte 5 SPA), Headless (`octo -p "..."`), IM (Feishu / Telegram / Discord / WeChat / WeCom / DingTalk) — five entry points, all running the same `agent.RunStream`. The entry layer's only job is to translate "user input" into a unified turn request and then translate agent events into their respective rendering formats (TUI cards, WebSocket messages, IM text chunks).

### 2. Assembly Layer: The Only Package Allowed to Know Everything

`internal/app/` is the only place that imports `agent`, `provider`, `permission`, `subagent_manager`, `mcp`, and `memorybackend` at the same time. Its job: adapt a Provider to `agent.Sender`, and build a `NewSessionToolEnv` (permission gate, browser, task store, goal store).

Clean separation: upper layers only talk to `app`; `app` assembles everything into what agent needs.

### 3. Agent Core: Doesn't Read HTTP, Doesn't Splice JSON

`Agent.RunStream` is a send → tool-dispatch → reply loop. It only knows that `Sender.SendMessagesToTools()` returns an abstract response. It has no idea about SSE, streaming tool_call fragment splicing, or the spelling differences between `finish_reason` and `stop_reason`.

All wire-format quirks are encapsulated in provider adapters: `internal/provider/anthropic` handles Messages API, `internal/provider/openai` handles Chat Completions. Even the `retry` package is isolated — provider-level request recovery and stream idle timeouts never pollute agent code.

### 4. Provider Normalization: The Same "tool_use"

Anthropic returns `stop_reason: "tool_use"`, OpenAI returns `finish_reason: "tool_calls"`. One job of the provider adapter is to normalize both into the unified "tool calls needed" signal. Similarly, cache-token bucketing differs by protocol: Anthropic uses `(input_tokens, cache_read_input_tokens)`, OpenAI uses `(prompt_tokens, cached_tokens)` — the agent always sees non-overlapping `InputTokens` and `CacheReadTokens` counters.

### 5. Tool System: One Line to Register, Zero Core Edits

Each tool implements the `ToolExecutor` interface + a `Definition()` method (returning the JSON Schema). Adding a tool means appending one line to `tools/allTools`. `DefaultRegistry.Execute` dispatches by name — MCP tools are injected with the `mcp__` prefix, built-ins run their own Go implementations. A permission gate (deny/ask/allow + interactive/auto/strict modes) intercepts before execution.

### 6. The Cross-Cutting "Invisible Champions"

Several packages that don't get their own layer but are everywhere:

- **`internal/permission/`** — deny > ask > allow, deny wins regardless of declaration order. Engine rebuilt per-turn; editing `permissions.yml` takes effect immediately. Parse failures silently fall back to `lastGoodRules` without crashing sessions.
- **`internal/skills/`** — L1 is just name + description + frontmatter — the smallest system-prompt budget. L2 body is loaded on demand via the `skill` tool, preventing skills from blowing up the context window.
- **`internal/memorybackend/`** — hindsight / mem0 / MemTensor semantic memory backends, swappable. The `eventSink` hook auto-writes conversations to memory on `EventStop`.
- **`internal/workflow/`** — mruby (wazero-wasm Fiber) × Go goroutine collaboration; write `agent/parallel/pipeline` composable workflows in Ruby DSL.

### 7. Server + Channel: Dual Event Exits

`internal/server` handles 80+ HTTP routes + WebSocket broadcast. Agent-produced `EventKind` events (text_delta, tool_started, tool_done, turn_done…) fan out in two directions:

- **Server path** → `ws_hub` broadcasts to all WebSocket subscribers
- **Channel path** → `UIController.Handler` adapts into IM messages (Telegram edit_message / Feishu card.action.trigger / WeChat text chunking)

IM ask buttons (Telegram inline_keyboard / Discord components / Feishu interactive card) map button presses back to `allow/always/deny` text, reusing the existing `isAffirmative` / `isAlways` logic — no new backend code required.

---

## One Turn's Lifetime

From the user hitting Enter in the TUI, to the output appearing on screen, there are five steps:

1. **Entry**: `turncore.go` receives the request → calls `RunStream()`
2. **Assemble turn environment**: `app.NewSessionToolEnv()` injects the permission gate + SubAgentManager + GoalStore + browser + memory
3. **Core loop**: `RunStream()` calls `SendMessagesToTools()` → LLM returns `tool_use` → permission deny/ask/allow → `DefaultRegistry.Execute` → results fed back to LLM → loop until `end_turn`
4. **Event broadcast**: each text_delta / tool_start / turn_done through `EventHandler` → TUI card rendering (ViewSink) + WS broadcast (server) + IM message (channel UIController)
5. **Session sync to disk**: `Session.SyncFrom(history)` appends to `~/.octo/sessions/<id>.jsonl`

---

## Quick Map: Where to Find Code

| To understand...            | Go to                                                          |
| ---------------------------- | -------------------------------------------------------------- |
| Permission decision logic    | `internal/permission/permission.go`                            |
| What happens on context overflow | `internal/agent/overflow.go` → `compaction.go`             |
| Where sessions live, what they look like | `internal/agent/session.go` + `~/.octo/sessions/`    |
| How sub-agents are spawned   | `internal/tools/subagent_manager/` + `internal/app/spawner.go` |
| WS protocol details          | `internal/server/ws_types.go`                                  |
| Adding a new provider        | Copy `internal/provider/anthropic/`, update routing in `app/sender.go` |
| Adding a new IM              | Implement `channel.Adapter` interface, self-register             |

---

## Next Steps

After your first `octo serve`, look right in the Web UI — you'll see this architecture run live: the event stream in the WebSocket panel, session writes, tool-call folding cards — each frame maps directly to the data flow drawn above.

Recommended reading path for backend engineers:

1. `cmd/octo/turncore.go` — what a single turn looks like
2. `internal/agent/agent.go` — `runLoop` + tool dispatch + senderMu
3. `internal/server/ws_handlers.go` → WS event conversion
4. `internal/provider/anthropic/stream.go` → SSE aggregation + stop_reason normalization
5. `internal/tools/terminal.go` — the canonical `ToolExecutor` reference
6. `internal/permission/permission.go` — how instant生效 works
7. `internal/workflow/runtime.go` — mruby Fiber × Go goroutine

Treat each layer as a black box — the full picture forms in ~90 minutes.
