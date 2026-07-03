---
title: 用 Hooks 自动化
description: 在 agent 生命周期的固定节点运行你自己的 shell 命令。
---

Hooks 会在生命周期的固定节点执行一条外部命令——这套模型来自 Claude Code，octo 把它搬到了每一种
传输方式上（CLI、Web、IM）。

## 七个事件

| 事件 | 触发时机 | 能否拦截 |
|---|---|---|
| `SessionStart` | 每次打开一个逻辑会话时（一次） | stdout 会折叠进上下文 |
| `UserPromptSubmit` | 每一轮用户消息之前 | stdout 会折叠进上下文 |
| `PreToolUse` | 每次工具调度之前 | 可以——能允许/拦截这次调用 |
| `PostToolUse` | 每次工具成功返回之后 | stdout 会折叠进上下文 |
| `Stop` | 一轮 assistant 回复结束时，无论成功还是出错 | 仅副作用 |
| `SubagentStop` | 一个子代理结束时 | 仅副作用 |
| `PreCompact` | 历史压缩之前 | 仅副作用 |

`PreToolUse` 是严格意义上唯一的门控点——它的退出码和 JSON 决策可以在工具调用真正执行前允许、
询问或拒绝，和普通的权限引擎组合生效（只能收紧，不能放宽）。其余的都是观察/副作用节点；
`SessionStart`、`UserPromptSubmit`、`PostToolUse` 还会把它们的 stdout 折叠回模型的上下文里。

## 配置位置

Hooks 按事件声明，相关的两个（`PreToolUse` / `PostToolUse`）还能按工具名匹配，并且在 CLI、
`octo serve` 的网页会话、以及每一个 IM 渠道上表现一致——一套引擎，覆盖所有传输方式。

下一步：一个常见搭配是在 `terminal` 上挂一个 `PostToolUse` hook，在 `git commit` 之后提醒保存记忆——
见[让它拥有记忆](/docs/zh/guides/memory/)。
