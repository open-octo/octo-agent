---
title: 兼容性与退出码
description: 哪些能放心依赖，哪些是 best-effort，哪些是内部实现。
---

octo 遵循[语义化版本](https://semver.org)。从 v1.0.0 开始，下面这些分层是硬承诺：破坏一个 Stable
层级的东西必须走大版本，并且一定在 [CHANGELOG](https://github.com/open-octo/octo-agent/blob/main/CHANGELOG.md)
里带上 **Breaking** 标注。

- **大版本（Major）** —— 一个 Stable 层级发生了不兼容变更，且已经过了下面的弃用窗口期。
- **小版本（Minor）** —— 新功能；对 Stable 层级的增量式修改；对 Best-effort 层级的任何修改。
- **修订版（Patch）** —— 修 bug；不涉及层级变化。

## Stable

| 层级 | 承诺内容 |
|---|---|
| `~/.octo/config.yml` | 已识别字段保持名字和含义不变；新字段都是可选的、有可用默认值；未知字段会被忽略 |
| `~/.octo/permissions.yml` | 规则格式（以工具名为 key 的 `allow`/`deny`/`ask` 列表，`pattern`/`hostname`/`path` 匹配器）；"第一条匹配即生效"的语义是承诺的一部分 |
| `~/.octo/mcp.json`, `.octo/mcp.json` | `mcpServers` 的结构（stdio 的 `command`/`args`/`env`，HTTP 的 `url`/`headers`/`auth`）；项目文件按名字覆盖用户文件 |
| `~/.octo/channels.yml` | 按平台名分组的 `channels` map，每个平台文档化的字段 |
| `SKILL.md` | YAML frontmatter + markdown 正文，Claude Code 的格式，从 `~/.octo/skills/` 和 `.octo/skills/` 发现 |
| `~/.octo/agents/<name>.md` | 自定义[子代理](/docs/zh/guides/sub-agents/)定义——`description`/`read_only`/`tools`/`disallowed_tools`/`model` frontmatter + 系统提示正文；文件名就是类型名 |
| `soul.md` / `user.md` / `octorules.md` / `.octorules` | 同样的分层顺序和 `@include` 支持 |
| `~/.octo/memories/<slug>/` | `MEMORY.md` 索引 + 按需加载的 topic 文件，纯 markdown |
| 会话（`~/.octo/sessions/*.jsonl`）、任务（`~/.octo/tasks/`） | **读取保证**：任何 1.x release 都能读懂更早的 1.x（以及 0.19+）写下的状态。降级不在保证范围内 |
| CLI 子命令与文档化的 flag | 名字和语义保持可用；改名会在弃用窗口期内继续支持旧拼写 |
| 退出码 | `0` 成功 · `1` 出错 · `2` 用法错误/未知的 help · `42` 来自 `octo serve`，表示"请求重启"（supervisor 契约） |
| `OCTO_*` 及各 vendor 的环境变量 | 含义保持不变；新增的都是增量式的 |

即使在 Stable 范围内，也不包括人类可读的 stdout/TUI 文本——不要去解析它。

## Best-effort

文档化且真实存在，但变更会随小版本发布并在 CHANGELOG 里说明，而不需要走大版本：

- **HTTP API（`/api/*`）和 WebSocket 事件**——完整参考见[这里](/docs/zh/reference/http-api/)。
  这是内置 Web UI 自己的 API，和 UI 打包在同一个二进制里，不可能跟 UI 产生分歧——但目前还没有
  带版本号的 `/api/v1`。
- **默认内容**——内置权限规则、默认 skill、prompt 组织方式。这些是行为调优，不是格式
  （它们所用的*格式*本身是 Stable 的）。
- `GET /api/health` / `GET /api/version` 会一直保持免认证、返回 JSON，但返回体可能会新增字段。

## Internal（内部实现）

`~/.octo/` 下所有未在上面提到的内容（`tmp/`、`logs/`、`bin/`、`trash/`、`uploads/`、
`mcp-tokens/`、`history/`、`skills-default/`）、以 `__` 开头的入口命令（`__complete`、
`__sandboxed-exec`、`__trash-backup`），以及 Go module 的 `internal/` 包——octo 是以二进制形式
分发的，不是作为库使用。

## 平台支持

Linux 和 macOS 是一等公民。有两个 Windows 上的差异在这里明确说明，而不是作为承诺：

- **交互式 `terminal_input` 仅 POSIX 可靠**——PowerShell 的 `-Command` 模式不能可靠地把重定向的
  stdin 转发给子进程。请改用 `terminal` 的 `stdin` 参数提前传入内容。
- **`--sandbox` 在 Windows 上不可用**——操作系统级限制只支持 Seatbelt/Landlock；它会直接
  fail closed，而不是不受限地运行。

## 迁移策略

旧格式在读取时会自动迁移——就像现在 `config.yaml` 自动升级成 `config.yml` 一样，原文件会保留
`.bak` 后缀。一个被弃用的格式或 flag 拼写，在 CHANGELOG 宣布之后至少还会在一个小版本里继续可用；
彻底移除对旧格式的读取支持是破坏性变更，只会发生在大版本里。

一个 Stable 层级在没有走大版本、或者 CHANGELOG 里没有 **Breaking** 标注的情况下被破坏，那就是一个
bug——请[提交 issue](https://github.com/open-octo/octo-agent/issues)。
