---
title: "octo-agent 架构深度解析：五层核心栈与全系统依赖图"
description: "一幅可交互的全景架构图，解析 octo-agent 从 CLI/IM 入口到 LLM 后端的五层单向依赖、agent 叶子包设计、协议无关抽象，以及 permission/workflow/memory 等跨切关注点。"
pubDate: 2026-07-08
updatedDate: 2026-07-10
author: "octo-agent team"
tags: ["architecture", "deep-dive", "engineering", "ai-agent"]
locale: zh
originalSlug: architecture-deep-dive
---

# octo-agent 架构深度解析：五层核心栈与全系统依赖图

> octo-agent 的代码仓库已经膨胀到 480+ Go 文件、六个 IM 桥接、一个内嵌 Svelte 5 工作台。本文用一幅可交互的全景架构图，看懂整个系统是怎么组织起来的。

---

## 设计哲学：严格单向依赖

octo-agent 最核心的架构准则是**依赖方向严格单向**。整个系统五层栈：

```
cmd/ → app/ → agent/ ← (provider/, tools/)
```

`internal/agent/` 是整个依赖树的**叶子包**——它不导入任何上层代码（不导入 `provider`、`tools`、`server`、`channel`、`web`）。所有协议细节、工具实现、UI 渲染都被"向上推"到更高层。

这意味着你可以：
- 替换 LLM 后端（anthropic → openai → deepseek）而不改一行 agent 代码
- 新增工具（terminal、browser、skill、workflow、MCP……）而不碰 agent 循环
- 更换渲染终端（TUI、Web IM、Headless）而不影响核心逻辑

这一条纪律，是 octo-agent 能持续膨胀的主要原因。

---

## 全景架构图

<iframe
  src="/blog/architecture-map.html"
  width="100%"
  height="1400"
  style="border: none; border-radius: 8px;"
  loading="lazy"
></iframe>

> 👆 点击每个卡片的标题区域，可展开查看关键文件路径和设计细节。

---

## 七个切面速览

### 1. 入口层：同一条 agentic 循环，多种面孔

TUI（Bubble Tea）、Web（Svelte 5 SPA）、Headless（`octo -p "..."`）、IM（飞书/Telegram/Discord/微信/企微/钉钉）——五种入口，底层跑的都是同一个 `agent.RunStream`。入口层唯一做的事是把"用户输入"翻译成统一的 turn 请求，然后把 agent 产出的事件翻译成各自的渲染格式（TUI 卡片、WebSocket 消息、IM 分片文本）。

### 2. 装配层：唯一有权限知道所有东西的包

`internal/app/` 是整个系统唯一同时导入 `agent`、`provider`、`permission`、`subagent_manager`、`mcp`、`memorybackend` 的地方。它负责把 Provider 适配成 `agent.Sender`，搭建 `NewSessionToolEnv`（包含权限闸门、浏览器、任务 store、goal store）。

职责分明：上层只和 `app` 交互；`app` 负责把一切组装成 agent 需要的样子。

### 3. Agent 核心：不读 HTTP、不拼 JSON

`Agent.RunStream` 是 send → tool-dispatch → reply 的循环。它只知道 `Sender.SendMessagesToTools()` 返回一个抽象响应；不知道 SSE、不知道 tool_call 流式拼接、不知道 `finish_reason` 和 `stop_reason` 的拼写差异。

所有 Wire 格式的怪癖都被封在 Provider 适配层：`internal/provider/anthropic` 处理 Messages API、`internal/provider/openai` 处理 Chat Completions。甚至 `retry` 包也是独立的——provider 级别的请求恢复和流式 idle 超时，完全不污染 agent。

### 4. Provider 归一化：同一个"tool_use"

Anthropic 返回 `stop_reason: "tool_use"`，OpenAI 返回 `finish_reason: "tool_calls"`。Provider 适配层的职责之一就是把它们归一化成统一的"需要调用工具"信号。同理，cache token 分桶在 Anthropic 协议是 `(input_tokens, cache_read_input_tokens)`，在 OpenAI 协议是 `(prompt_tokens, cached_tokens)`，agent 看到的始终是统一的 `InputTokens` 和 `CacheReadTokens` 不重叠计数。

### 5. 工具系统：一行注册，不改核心

每个工具实现 `ToolExecutor` 接口 + `Definition()` 方法（返回 JSON Schema）。新增工具只需往 `tools/allTools` 加一行。`DefaultRegistry.Execute` 按名分发——MCP 工具以 `mcp__` 前缀注入、内置工具走自己的 Go 实现。权限闸门（deny/ask/allow + interactive/auto/strict 三 mode）在工具执行前拦截。

### 6. 跨切关注点的"隐形冠军"

几个不单独成片但无处不在的包：

- **`internal/permission/`** — deny > ask > allow，deny 优先级最高，无视声明顺序。每 turn 重建引擎，改 `permissions.yml` 立即生效。解析失败时安静回退到 `lastGoodRules`，不会绷掉 session。
- **`internal/skills/`** — L1 只有 name + description + frontmatter，注入 system prompt 的 budget 最小；L2 的 body 通过 `skill` 工具按需加载，避免 skill 冲爆上下文窗口。
- **`internal/memorybackend/`** — hindsight / mem0 / MemTensor 三种语义记忆后端可选。`eventSink` hook 在 `EventStop` 时自动将对话写入记忆。
- **`internal/workflow/`** — mruby（wazero-wasm Fiber）+ Go goroutine 协作，用 Ruby DSL 写 `agent/parallel/pipeline` 组合式工作流。

### 7. Server + Channel：事件双重出口

`internal/server` 处理 HTTP 路由（80+）+ WebSocket broadcast。Agent 产出的 `EventKind` 事件（text_delta、tool_started、tool_done、turn_done……）被分发两个方向：

- **Server 路径** → `ws_hub` 广播给所有 WebSocket 订阅者
- **Channel 路径** → `UIController.Handler` 适配成 IM 消息（Telegram edit_message / Feishu card.action.trigger / WeChat 纯文本分包）

IM 的 ask 按钮（Telegram inline_keyboard / Discord components / Feishu interactive card）按下后映射为 `allow/always/deny` 文本，复用已有的 `isAffirmative` / `isAlways` 判断逻辑，完全不需要新的后端路径。

---

## 一个 turn 的生命周期

从用户在 TUI 敲下回车，到结果出现在屏幕上，一共五步：

1. **入口**：`turncore.go` 接收请求 → 调 `RunStream()`
2. **装配 turn 环境**：`app.NewSessionToolEnv()` 注入权限闸门 + SubAgentManager + GoalStore + browser + memory
3. **核心循环**：`RunStream()` 调 `SendMessagesToTools()` → LLM 返回 `tool_use` → permission deny/ask/allow → `DefaultRegistry.Execute` → 结果回喂 LLM → 循环直到 `end_turn`
4. **事件广播**：每次 text_delta / tool_start / turn_done 经 `EventHandler` → TUI card 渲染（ViewSink）+ WS broadcast（server）+ IM 消息（channel UIController）
5. **Session 落盘**：`Session.SyncFrom(history)` 追加到 `~/.octo/sessions/<id>.jsonl`

---

## 速查：找代码去哪

| 想了解...                   | 去哪看                                                          |
| -------------------------- | --------------------------------------------------------------- |
| 权限判定逻辑               | `internal/permission/permission.go`                             |
| 上下文溢出怎么办           | `internal/agent/overflow.go` → `compaction.go`                  |
| session 存在哪、长什么样   | `internal/agent/session.go` + `~/.octo/sessions/`               |
| 子 agent 怎么 spawn        | `internal/tools/subagent_manager/` + `internal/app/spawner.go`  |
| WS 协议细节                | `internal/server/ws_types.go`                                   |
| 加新 provider              | 复制 `internal/provider/anthropic/`、改 `app/sender.go` 路由    |
| 加新 IM                   | 实现 `channel.Adapter` 接口自注册                               |

---

## 下一步

你第一次启动 `octo serve` 后，Web UI 右侧就能看到这套架构在实时运行：WebSocket 面板里的 event 流、session 写入、工具调用折叠卡片——每一帧都对应上图里画的那条数据流。

后端工程师推荐阅读顺序：

1. `cmd/octo/turncore.go` — 单 turn 长什么样
2. `internal/agent/agent.go` — `runLoop` + tool dispatch + senderMu
3. `internal/server/ws_handlers.go` → WS 事件转换
4. `internal/provider/anthropic/stream.go` → SSE 聚合 + stop_reason 归一化
5. `internal/tools/terminal.go` — 公认的 ToolExecutor 典范
6. `internal/permission/permission.go` — 即时生效的奥秘
7. `internal/workflow/runtime.go` — mruby Fiber × Go goroutine

每层当黑盒用，整体认识 ~90 分钟形成。
