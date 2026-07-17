---
title: "为什么到今天我又做了一个开源 Agent"
description: "Octo 是一个单 Go 二进制、自托管的个人 AI Agent。这是它的起源故事：为什么做、关键设计取舍，以及它刻意不做什么。"
pubDate: 2026-07-17
author: "Roy Lei"
tags: ["announcement", "octo-agent", "story"]
locale: zh
originalSlug: why-octo
---

# 为什么到今天我又做了一个开源 Agent

Octo 是一个个人 AI Agent，打成一个 Go 二进制发布：34 个内置工具、20 个默认技能，8 个规划界面已上线 7 个（CLI、Web、桌面、IM 桥接、VS Code、Obsidian、Go SDK——移动端在做）。安装就是一行命令，不依赖 Node、Python、Ruby：

```bash
curl -fsSL https://octo-agent.dev/install.sh | sh
octo config
```

## 为什么还要做

两个原因。

第一，我真心受够了分裂的工作流——写代码用 Claude Code，日常任务用 OpenClaw。两个工具，两套配置，两种体验，而它们本质上是同一件事：和一个有工具的 Agent 对话。这种不满贯穿了 Octo 的每一个设计决策。

第二，第一次深度使用 Claude Code 时，我被它背后发生的事情震撼了——一个 AI 在终端里读写文件、执行命令、自我纠错。我想搞懂这一切到底是怎么运作的，而理解一个系统最好的方式就是从零造一个。有 AI 帮忙写代码，一个人做这件事变成了可能。

## 一种会话，没有模式

市面上的 Agent 总把 Chat、Coding、Work 拆成不同的模式或应用。Octo 不拆。这三者本质上是同一件事——和一个有工具的 Agent 对话——所以 Octo 只有一种会话，做所有事。你永远不用停下来想"我现在该用哪个模式"。

同样的逻辑也消除了"Coding Agent"和"General Agent"的划分。一个通用的个人助手，当然应该会写代码、会做 PPT，没理由拆成两个产品。

## 几个值得说的设计决策

**MCP 不撑爆上下文。** Octo 支持 stdio、Streamable HTTP、OAuth 三种 MCP Server，但工具 schema 按需加载——内置的 Tool Search 桥让模型按名查找工具，而不是每轮对话都背着全部 schema。MCP Server 随便加，上下文窗口不会爆。

**Agent 拆不了自己的家。** Octo 是编译型二进制，Agent 没法像 Node/Python 源码安装类 Agent 那样修改自己的源码。配置修改写入时会校验，坏配置永远不生效——最后一份好配置继续工作。Agent 也杀不掉 Octo server 进程，即使你让它这么做：server 自己的 PID 在工具层就被保护住了。

**删除默认可恢复。** 模型偶尔会干出破坏性操作。Octo 里 `rm`、覆盖写入、程序化删除全部先进本地回收站（保留 14 天，有容量上限），CLI 或 Web UI 随时找回。如果文件被 git 追踪且干净，备份直接跳过——git 已经有了。

**浏览器自动化只接管，不新起。** 浏览器工具通过 CDP 驱动你已登录的真实 Chrome，而不是新起一个没 cookie 的 headless 实例。录制的操作会编译成基于语义选择器的 YAML 技能，前端改版时能自愈。

## 隐私

Octo 零遥测。除了你显式发出的模型请求，没有任何东西离开你的机器。你也可以指向本地模型——两种协议（Anthropic 和 OpenAI 风格）以及任何兼容端点都支持。

## 它（还）不做什么

默认**没有 OS 级隔离**——权限引擎按规则拦截命令，真正的沙箱（`--sandbox`，macOS 用 Seatbelt，Linux 用 Landlock + seccomp）是可选开启的，Windows 上不可用。移动客户端还没发布。一个人做的 Agent 在绝对能力上也赢不了顶级厂商的 Agent——Octo 的竞争力在于自托管、隐私，以及所有东西都在一个地方。

## 站在巨人的肩膀上

Octo 离不开这些优秀的开源项目：Claude Code（Loop 和 Skill 体系的设计灵感）、Codex（Goal 机制的参考来源）、OpenClaw（个人助手形态的先行者）、Hermes（Agent 交互的另一种优秀探索）、OpenClacky（Octo 的 Web 端交互受它启发）。
