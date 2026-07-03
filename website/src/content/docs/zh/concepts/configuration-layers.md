---
title: 配置分层
description: octo 是如何从身份、画像和规则文件里拼出系统提示的。
---

octo 的系统提示由几个可选的层组成——后面的层会覆盖/扩展前面的：

| 层 | 作用域 | 用途 |
|---|---|---|
| `~/.octo/soul.md` | 全局 | agent 的身份与行为方式，一种 openclaw/hermes 风格的人格设定 |
| `~/.octo/user.md` | 全局 | 你是谁——每次会话都会注入的个人画像 |
| `~/.octo/octorules.md` | 全局 | 你跨项目的规则和偏好 |
| `.octorules` | 单仓库 | 随仓库一起提交的项目约定 |
| `--system "..."` | 单次 | 仅本次运行生效的覆盖 |

用 `octo init`（或 TUI 里的 `/init`）可以为当前仓库生成一份起始的 `.octorules`——它会先分析代码库，
再起草约定，而不是给你一个空文件。

## `@include`

身份文件和规则文件都支持 `@include path/to/fragment.md` 引入共享内容——适合在多个相关仓库的
`.octorules` 里复用同一段片段。

下一步：记忆系统在这之上再叠加了一层独立的、按仓库存放的索引——见
[让它拥有记忆](/docs/zh/guides/memory/)。
