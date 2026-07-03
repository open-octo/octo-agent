---
title: 常见问题
description: 一些常见问题。
---

### 我的数据会离开这台机器吗？

只有你明确发给你所选模型 provider（Anthropic、OpenAI，或者你配置的任何端点）的对话内容会出去。
会话、记忆和配置都存在运行 `octo` 的这台机器的 `~/.octo/` 下面——中间没有一个 octo 官方运营的
后端服务。

### 有托管版本吗？

没有——自托管就是这个项目的重点。`octo serve` 就是你已经在用的那个二进制；想把它暴露到自己的
机器之外，见[自托管 octo serve](/docs/zh/guides/self-host/)。

### 我能复用 Claude Code 的 skill 吗？

可以。`SKILL.md` 格式完全一致，把 `~/.claude/skills` 软链接到 `~/.octo/skills`，
你已经有的东西立刻就能用——见[使用 Skills](/docs/zh/guides/use-skills/)。

### 这个和 Claude Code / Codex CLI / Hermes 有什么区别？

不是功能清单式的对比——见[文档首页](/docs/zh/)上的对比表。简单说：同一类工具，但 MIT 开源、
单个 Go 二进制没有运行时依赖，而且可以免费接任何支持 Anthropic 或 OpenAI 协议的模型。

### `--sandbox` 在 Windows 上能用吗？

不能——操作系统级限制只支持 macOS Seatbelt / Linux Landlock，`--sandbox` 在 Windows 上会直接
fail closed（拒绝运行），而不是假装限制了什么。那边靠交互式权限引擎兜底。见
[沙箱化运行](/docs/zh/guides/sandbox-the-agent/)。

### 如果 octo 在任务中途崩溃或者断网了怎么办？

会话历史按"轮"为粒度持久化，最多丢掉正在进行的这一轮，而不是整个会话——用 `octo -c` 恢复。
见[会话与历史](/docs/zh/concepts/sessions-and-history/)。

### 我把 `octo serve` 绑到了 localhost 之外，结果什么都用不了

非回环绑定要求每个请求都带访问密钥。启动时会打印一个带 key 的、可以直接打开的 URL；见
[自托管 octo serve](/docs/zh/guides/self-host/)和[安全模型](/docs/zh/reference/security/)。

### 在哪里报 bug 或者提功能需求？

去 GitHub [提交 issue](https://github.com/open-octo/octo-agent/issues)。如果是安全漏洞，
请用[私密安全公告](https://github.com/open-octo/octo-agent/security/advisories/new)，
不要发公开 issue。
