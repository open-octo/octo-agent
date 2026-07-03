---
title: 使用 Skills
description: 以 Claude Code 的 SKILL.md 格式提供的可复用、按需加载的指令集。
---

Skills 是可复用的指令集，模型只在任务匹配时才加载它们——不匹配的轮次完全不占用上下文。

## Skills 放在哪

- `~/.octo/skills/<name>/SKILL.md` —— 用户级，所有项目共用。
- `.octo/skills/<name>/SKILL.md` —— 项目级，优先级高于用户级。

格式和 Claude Code 完全一致，所以你可以把 `~/.claude/skills` 软链接到 `~/.octo/skills`，
直接复用你已经有的一切：

```bash
ln -s ~/.claude/skills ~/.octo/skills
```

## 一个 skill 长什么样

每个 `SKILL.md` 是 YAML frontmatter 加一段 markdown 正文：

```markdown
---
name: review
description: Review the current diff for correctness and style
---
Walk the diff hunk by hunk and flag correctness bugs first, then style.
```

会话开始时，octo 会把每个 skill 的名字和描述列进系统提示——只是一行清单，不是完整正文。只有当任务匹配时，
模型才会通过 `skill` 工具按需加载这个 skill 的完整指令。

## 使用 skills

```bash
octo skills list     # 看看发现了哪些 skill
```

在 TUI 里：`/skills` 列出所有 skill，`/<name>`（比如 `/review`）直接运行某一个。

下一步：skill 经常需要用到 MCP 工具——见[接入 MCP 服务](/docs/zh/guides/connect-mcp-servers/)。
