---
title: 系统分层
description: 五层、单向依赖的架构。
---

```
cmd/octo/          CLI 入口（单发对话 + TUI、serve、mcp、slash 命令）
   ↓
internal/agent/    历史记录、会话、内容 block、Sender 接口、
                   Agent.Turn / TurnStream / Run（工具调用循环）
   ↓
internal/provider/ Provider 接口及具体实现
                   ├─ anthropic/   x-api-key、system 顶层字段、content[].text
                   └─ openai/      Bearer 认证、system 放在 messages[0]
   ↓
internal/tools/    ToolExecutor 实现——terminal（含后台）、
                   文件读/写/改、glob、grep、web 抓取/搜索、skill
internal/skills/   SKILL.md 发现 + 系统提示清单
internal/permission/  给每次工具调用做门控的 allow/deny/ask 规则引擎
internal/mcp/      MCP 客户端（stdio + HTTP，OAuth）
internal/server/   octo serve —— HTTP REST + SSE + 内置控制台
internal/channel/  IM 桥接——适配器接口 + 微信 iLink / 飞书 /
                   钉钉 / 企微 / Discord / Telegram 各适配器
```

依赖方向是强制执行的，不只是写在文档里：`provider` 从不 import `agent`，`agent` 也从不
import `provider`——agent 循环是针对 `Sender` 接口写的，`internal/app` 是唯一构造具体
provider 客户端、并把它作为该接口交给 agent 的地方。

## `Sender` 接口栈

每个 provider 都同时实现缓冲式（`Send`）和流式（`SendStream`）两个版本。agent 层用一组层层
叠加的接口来镜像这一点：

```
Sender → StreamingSender → ToolSender → ToolStreamingSender
```

调用方会类型断言出某个 provider 实际支持的最高能力，所以一个不支持流式或不支持工具调用的
provider 依然能工作——只是能力少一些，而不是编译报错。

## App 启动层

`internal/app` 是唯一构造 provider 客户端、并把它适配成 `agent.Sender` 的地方。每一个入口——
`cmd/octo`、`internal/server`、各个 IM 渠道——都通过它去接触 LLM，而不是直接 import
`provider`。它还负责权限门、子代理启动器，以及 MCP + 内置工具的统一（`WireTools`），
让这三个能力在 CLI、Web 服务和每一个聊天适配器之间保持一致，而不是各自演化走偏。

下一步：[Provider 协议](/docs/zh/architecture/provider-protocols/)介绍了 `anthropic/` 和
`openai/` 各自把哪些东西从 agent 层屏蔽掉了。
