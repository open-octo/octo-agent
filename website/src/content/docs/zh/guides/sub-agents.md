---
title: 子代理
description: fork 当前对话，或者启动一个独立的、专门化的 agent。
---

`sub_agent` 工具会启动一个全新的 agent——可以是当前对话的一份拷贝（fork），也可以是一个带着自己
的人格和工具集、完全独立的 agent。这里没有对应的 slash 命令：任务需要隔离或者需要一个专门视角时，
模型会直接调用这个工具。

## Fork 还是走类型

| | Fork（不指定类型） | 指定类型（`subagent_type`） |
|---|---|---|
| 历史 | 拷贝父对话到目前为止的全部内容 | 从空白开始 |
| 系统提示 | 和父 agent 一样——共享它的 prompt cache，所以很便宜 | 父 agent 的提示加上该预置类型的人格文本 |
| 模型 | 父 agent 的模型，除非被覆盖 | 父 agent 的模型，除非预置类型或调用本身覆盖了它 |
| 工具 | 父 agent 的完整工具集，去掉 `sub_agent` 本身 | 父 agent 的工具集，按预置类型的 `tools`/`disallowed_tools` 过滤 |
| 适合 | 把一次嘈杂的探索甩出去，只留结论 | 需要一个独立视角或专门角色 |

递归被硬性限制在一层：子代理自己的工具集里永远不包含 `sub_agent`，就算有什么绕过了这层过滤，
调用它也会被直接拒绝。

## 内置类型

| 类型 | 只读 | 说明 |
|---|---|---|
| `explore` | 是 | 快速调研；配置了更便宜的 lite model 时会跑在那上面 |
| `plan` | 是 | 只读式调查，产出一份实施计划；同样跑 lite model |
| `general` | 否 | 完整工具集，用于端到端委派 |
| `code-review` | 是 | 通过 `git diff` 做 review |

`explore` 和 `plan` 是仅有的两个"lean"类型——除了跑 lite model，它们的系统提示里还会去掉 skills
清单和记忆注入，毕竟一次快速调研通常两者都用不上。

## 自定义类型

在 `~/.octo/agents/<name>.md`（用户级）或 `.octo/agents/<name>.md`（项目级，同名时优先）放一个
markdown 文件，它就变成一个模型可以按文件名请求的 `subagent_type`：

```markdown
---
description: Audits code for security vulnerabilities
read_only: true
tools: [read_file, grep, glob, terminal]
disallowed_tools: [write_file, edit_file]
model: inherit
---
You are a security-focused sub-agent. Review the diff for OWASP top 10 issues, hard-coded
secrets, and injection risks. Report file:line findings with severity — don't modify anything.
```

| 字段 | 是否必填 | 含义 |
|---|---|---|
| `description` | 是 | 给模型看的，让它知道什么时候该用这个类型 |
| `read_only` | 否 | 限制只能用不产生副作用的工具 |
| `tools` | 否 | 显式白名单 |
| `disallowed_tools` | 否 | 从继承来的工具集里减掉这些 |
| `model` | 否 | `inherit`（默认）或者写死一个具体 model id |

frontmatter 里的 `name` 字段就算写了也会被忽略——文件名（去掉 `.md`）才是模型用来请求这个类型的
标识。目录每次查找都会重新扫描，改完文件立刻生效，不需要重启。

## 跟进一个异步子代理

启动可以是同步的，也可以是异步的。一个异步子代理只要没被杀掉、正常结束，就会保持可寻址：

| 工具 | 作用 |
|---|---|
| `sub_agent_send` | 给一个正在运行或已经结束的异步子代理发送后续消息 |
| `sub_agent_status` | 不阻塞地查看进度 |
| `sub_agent_kill` | 提前停止一个子代理 |

下一步：确定性地编排一整支子代理队伍，而不是一个一个来，正是[工作流](/docs/zh/guides/workflows/)要做的事。
