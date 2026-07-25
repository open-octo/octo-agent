---
title: 专家智能体
description: 创建和使用专精智能体——每个拥有独立的角色、工具集和技能——应对不同类型的工作。
---

专家智能体让你把工作划分到不同专精角色上。与其让一个智能体同时处理代码审查、调试、
调研和文档撰写，不如为每种角色各创建一个——每个智能体有自己的 system prompt、
工具白名单和技能集合。

## 快速上手

通过 Web UI 或对话创建代码审查智能体：

```bash
# CLI 方式
octo chat --agent code-review "review the diff"
```

Web UI 也有 **Agents** 面板（`/agents`）用于创建、编辑和删除专家智能体，
还可以把它们绑定到 IM 频道。

## 创建智能体

### Web UI

1. 打开 **Agents** 面板（侧边栏 → Agents，或按 `Cmd+K` → "Agents"）。
2. 点击 **New Agent**。
3. 填写：
   - **Name** — 在侧边栏和智能体选择器中显示的名称。
   - **Description** — 在列表中显示的描述，必填。
   - **System Prompt** — 角色定义。描述这个智能体是什么、做什么、怎么做。
     这会**替换** Default Agent 的身份层（soul.md、user.md）。
   - **Model** — 留空使用默认模型，或指定特定模型。
   - **Tools** — 勾选此智能体可以使用的工具。全部不勾选 = 授予全部工具
     （Default Agent 行为）。
   - **Skills** — 勾选此智能体可加载的技能。
4. 点击 **Save**。

### 对话中创建

直接对 Default Agent 说：

> 帮我创建一个代码审查智能体，只需要只读文件权限，加上 code-review 技能。

Default Agent 会通过 `expert-agent-manager` 技能调用 REST API 完成创建。

## 专家智能体与 Default Agent 的区别

| 维度 | Default Agent | Expert Agent |
|------|:---:|:---:|
| 角色 | `~/.octo/soul.md` + `~/.octo/user.md` | profile 的 system prompt（独立） |
| 用户规则 | `~/.octo/octorules.md` + `.octorules` | ❌ 不注入 |
| 工具 | 完整工具集 | profile 白名单 |
| 技能 | 全部可用技能 | profile 白名单；系统技能自动隐藏 |
| MCP 服务 | 全部连接 | profile 白名单 |
| 会话池 | 独立池 | 独立池，通过 `<agentID>#` key 前缀隔离 |
| 管理权限 | 创建/编辑/删除专家智能体 | 不能修改 profile 或安装技能 |

核心原则：**专家智能体的 system prompt 是独立的**。它不会继承 Default Agent
的角色（soul.md）、用户档案（user.md）或用户规则（octorules.md）。如果你希望
专家智能体也遵循这些规则，需要直接写进它的 system prompt 中。

## 使用专家智能体

### Web — 新建会话时选择

侧边栏"新会话"按钮右侧有一个下拉箭头（▼），点击可选择由哪个智能体处理新会话。
会话创建后**永久绑定**给选定的智能体——要切换智能体，开一个新会话即可。

侧边栏中每条会话都标记了所属智能体的标签。

### IM 频道 — @ 提及与频道绑定

把专家智能体绑定到频道后，消息会自动路由到它：

```
/bind code-review
```

在绑定了多个智能体的群聊中，用 `@review` 指定目标：

```
@review 帮我看看这个 diff
```

没有频道绑定的智能体可以通过 `@mention` 在任何 bot 所在的聊天中被呼叫。

### CLI

```bash
# 启动交互会话
octo --agent code-review

# 一次性任务
octo --agent code-review -p "review the latest commit"

# 列出可用智能体
octo --agent
# → agent "code-review" not found (available: default, code-review, ops-helper)
```

### TUI

在交互会话中使用 `/agent` 命令：

```
/agent                # 列出可用智能体
/agent code-review    # 切换到 code-review（创建新会话）
```

## 单智能体工具与技能过滤

创建专家智能体时，你可以选择它能使用哪些工具和技能。运行时：

- **工具**每轮自动过滤——专家智能体只看得到 allowlist 中的工具。
  空 allowlist = 全部工具可用（同 Default Agent）。
- **技能**在 system prompt manifest 中按 profile 过滤。系统级技能
  （`skill-creator`、`expert-agent-manager`、`channel-manager` 等）自动对
  专家智能体隐藏——它们只对 Default Agent 有意义。
- **浏览器录制的技能**在智能体的 allowlist 不含 `browser` 工具时自动隐藏。

## 智能体会话隔离

每个智能体有独立的会话池，通过 `<agentID>#` 前缀区分。这意味着：

- 绑定到 `code-review` 的聊天有自己独立的 history、`/bind`、`/list`、`/stop`
  ——与同聊天中 Default Agent 的会话互不干扰。
- 已有会话不会迁移，始终归属于创建它的智能体。
- 多智能体升级前的旧会话归属于 Default Agent。

## 子智能体可使用专家 profile

`sub_agent` 工具接受 `subagent_type` 参数来启动具有特定 profile 能力集的子智能体：

```
sub_agent(subagent_type: "code-review", prompt: "review the diff")
```

它会启动一个全新的、临时的子智能体，使用 code-review profile 的 tool 和 system
prompt——不会进入该智能体的会话池。

## Cron 任务的智能体归属

cron 任务归属于创建它的智能体。在 Web UI 的 Tasks 面板中：

- **Agent** 列显示每个 cron 任务归属于哪个智能体。
- Default Agent 用户可看到 **Transfer** 操作，可将任务转移给其他智能体。
- 通过 `?agent_id=` 参数过滤特定智能体的任务。

## 最佳实践

- **一个智能体、一件事**：叫"code-review"而不是"dev-ops-review-writer"。
  窄角色的 prompt 更短、效果更好。
- **system prompt 先写清晰**：两三句话描述这个智能体是什么、不是什么。
  后续随时可以优化。
- **限制工具**：只勾选智能体真正需要的工具。只做代码审查的智能体不需要
  `terminal` 或 `write_file`。
- **只给一两个技能**：技能描述是模型的触发信号——不要淹没它。
- **先用 one-shot 测试**：绑定到频道前，先 `octo --agent new-agent -p "测试提示"`
  看看响应是否符合预期。
