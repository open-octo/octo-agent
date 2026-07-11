---
title: 使用 Skills
description: 以 Claude Code 的 SKILL.md 格式提供的可复用、按需加载的指令集。
---

Skills 是可复用的指令集，模型只在任务匹配时才加载它们——不匹配的轮次完全不占用上下文。

## Skills 放在哪

- `~/.octo/skills-default/` —— 下面这批内置 skill 的落地位置，首次运行时从二进制里释放出来
  （升级后用 `octo skills update` 重新同步）。单独放一个目录，是为了刷新内置 skill 永远不会碰到
  你自己写的那些。
- `~/.octo/skills/<name>/SKILL.md` —— 用户级，所有项目共用。同名会覆盖内置 skill。
- `.octo/skills/<name>/SKILL.md` —— 项目级，同名时覆盖前两者。

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
octo skills path     # 打印用户级/项目级/内置 skill 目录
octo skills add      # 从某个来源引导安装一个 skill
octo skills update   # 升级后重新同步内置 skill
```

在 TUI 里，`/skills` 列出所有 skill，`/<name>`（比如 `/review`）直接运行某一个；Web UI 的输入框
`/` 菜单效果一样。**IM 渠道不支持 `/<skill-name>` 这种触发方式**——那边任何 `/文本` 都只会匹配固定的
[slash 命令](/docs/zh/reference/slash-commands/)集合，匹配不上就回"Unknown command"，不会落到某个
skill 上；直接用大白话说想干什么就行，模型会自己判断该加载哪个 skill。

## 内置 skills

octo 开箱自带 20 个 skill。每一个都会在模型判断任务匹配时自动触发——大多数情况下你不需要按名字调用它们。

**上手起步**

| Skill | 作用 |
|---|---|
| `onboard` | 首次运行的引导流程（起名字、定性格、填个人画像 → 写进 `soul.md` + `user.md`）；也支持用 `scope:soul`、`scope:user`，或某个具体的记忆文件路径做更窄范围的修订 |
| `product-help` | 通过读取 octo 自己的产品文档回答"怎么用 XXX"这类关于 octo 本身的问题 |
| `skill-creator` | 把一个可复用的任务写成新的 `SKILL.md`，或者改进已有的 |
| `workflow-creator` | 把**已有**的 skill 和浏览器录制串成一个可运行、可保存的[工作流](/docs/zh/guides/workflows/) |

**写代码、交付**

| Skill | 作用 |
|---|---|
| `tech-design` | 从 PRD 或功能描述出发，产出一份完整的后端技术方案文档 |
| `grill-me` | 就一个方案反复追问你，直到每个悬而未决的点都定下来——和 `tech-design` 搭配用 |
| `implement` | 把一份技术方案拆成有依赖顺序的若干片段，逐片段用 TDD 落地、用子代理 review，并把进度存盘，重启也不丢 |
| `code-review` | 用一个隔离的子代理 review 当前的 diff，给出不受主对话影响的正确性/规范/安全反馈 |
| `worktree-isolate` | 在一个隔离的 git worktree 里做有风险的改动，之后再决定合并还是丢弃 |

**自动化与调度**

| Skill | 作用 |
|---|---|
| `loop` | 在当前会话里重复跑一个 prompt——固定间隔或自己判断节奏——不用每次重新输入 |
| `cron-task-creator` | 创建/查看/编辑/删除能扛得住重启的定期任务，由 `octo serve` 的调度器执行 |

**接入外部系统**

| Skill | 作用 |
|---|---|
| `mcp-creator` | 找到合适的 MCP 服务，写好 `mcp.json` 配置项，并验证连接 |
| `channel-manager` | 引导你配置一个 IM 平台（飞书、微信、企微、钉钉、Discord、Telegram），写好 `channels.yml` |

**文档、媒体与调研**

| Skill | 作用 |
|---|---|
| `deep-research` | 多来源、经过事实核查的调研——扇出搜索、读一手资料、对每条结论做对抗性验证，最后合成一份带引用的报告 |
| `web-access` | 应对难搞网页目标的方法论 + 跨 session 的经验库：需要登录/反爬的站点、结构未知的页面、多来源交叉核实 |
| `office-xlsx` | 创建/读取/编辑 `.xlsx` 表格——公式、样式、合并单元格、多个 sheet、图表、数据校验 |
| `ppt-master` | 把文档（PDF/DOCX/URL/Markdown）转成可编辑的 PowerPoint——SVG 生成的幻灯片、原生图表/表格、演讲者备注，导出 `.pptx` |
| `image-gen` | 用 AI 模型（14 个后端）生成图片，或搜索开放版权素材，产出到文件——支持单张或批量；`ppt-master` 等 skill 会委托它生图 |

下一步：把这几个串成一条保存下来的流程，正是[工作流](/docs/zh/guides/workflows/)要做的事。
