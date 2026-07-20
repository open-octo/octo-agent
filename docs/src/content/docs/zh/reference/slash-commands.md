---
title: Slash 命令
description: 每一个 / 命令，以及它们在 TUI、Web UI、IM 渠道之间的差异。
---

三个界面并不共用一套命令——TUI、Web UI 的打字聊天框、以及 IM 渠道各有各的列表，因为每个界面的会话
语义不一样（TUI 的会话就是这个进程本身；一个 IM 聊天则可以被重新绑定到完全不同的会话上）。

## TUI

| 命令 | 语法 | 行为 |
|---|---|---|
| `/help` | `/help` | 打印命令列表和按键提示 |
| `/model` | `/model <name>` | 按名字切换到另一个已配置的模型——它所在端点的 provider 和连接会一起带上——并为它重建工具集 |
| `/thinking` | `/thinking off\|low\|medium\|high\|xhigh\|max` | 设置推理强度；会重建 sender，因为思考预算是在构造时确定的 |
| `/compact` | `/compact` | 立即压缩历史；有轮次在跑时会被拒绝 |
| `/clear` | `/clear` | 清空历史并立即保存；跑到一半时会被拒绝。用的还是同一个会话文件——想要全新的一个，见下面 IM 专属的 `/new` |
| `/goal` | 见[运行长周期目标](/docs/zh/guides/goals/) | 这里的 `/goal edit` 是预填模式——具体见那篇文档里的说明 |
| `/loop` | `/loop [间隔] <任务>` | 不是 router 命令：消息原样透传给模型，由模型按约定调用 `schedule_wakeup` 工具处理——见[跑一个循环任务](/docs/zh/guides/loop/) |
| `/skills` | `/skills` | 列出已发现的 skill，附上来源和描述 |
| `/mcp` | `/mcp` | 列出已连接的 MCP 服务——工具/资源/prompt 数量、服务的说明文字 |
| `/workflows` | `/workflows` | 列出具名工作流（内置、用户、项目三层）；跑某一个是靠描述任务，不是靠 slash 调用 |
| `/memory` | `/memory` | 列出记忆目录下的文件及其大小 |
| `/init` | `/init` | 跑一次完整的、带工具的轮次，生成或更新 `.octorules` |
| `/save` | `/save` | 立即保存会话，打印文件路径 |
| `/sessions` | `/sessions` | 列出最近的 10 个会话 |
| `/exit`、`/quit` | | 退出（等同 Ctrl-C / Ctrl-D） |
| `/<skill-name>` | `/<name> [args]` | 任何没有被上面这些保留命令占用名字的已发现 skill，会作为普通 `/<name>` 文本发出，由模型通过 `skill` 工具加载（需要工具——无工具时会被拒绝） |

不认识的 `/xxx` 会原样当作纯文本发给模型——对那些碰巧以斜杠开头的路径或正则表达式很友好。

## Web UI（打字聊天）

打字聊天文本里只解析这三个命令；其余的都原样发给模型当纯文本（Web UI 里切模型/切 skill/切
workflow 是走专门的按钮和一个由 `/` 触发的选择菜单，不是解析命令文本）：

| 命令 | 行为 |
|---|---|
| `/clear` | 清空这个会话的消息、保存、丢掉缓存的 agent 和记忆锁存，广播一次历史重载 |
| `/compact` | 在后台压缩（注册成可被中断/停止取消的任务） |
| `/goal [...]` | 和 IM 用的是同一套底层实现——这里 `/goal edit <文本>` 一步到位直接改 |

输入框自己的 `/` 自动补全是一个 skill/workflow/MCP 工具与服务的选择菜单，不是一份固定命令列表。
选中某一项只是把 `/<name> ` 填进输入框，之后走的还是和 TUI 一样的服务端 skill 派发逻辑。

## IM 渠道

IM 会话可以在不同聊天之间重新绑定，所以这个界面有其他两个都没有的命令：

| 命令 | 语法 | 行为 |
|---|---|---|
| `/bind` | `/bind [--force] <序号\|id>` | 把这个聊天绑定到一个已有会话上，按 `/list` 里显示的序号或者短/完整 id。历史会保留。`--force` 可以抢一个被别的聊天占着、但租约已过期的绑定 |
| `/unbind` | `/unbind` | 把这个聊天从它的会话上解绑，不删任何东西 |
| `/new` | `/new` | 新建一个全新会话并绑定这个聊天——这是唯一一种不动已有会话历史、直接另起一个的方式 |
| `/clear` | `/clear` | 清空历史，但保留当前的绑定关系 |
| `/compact` | `/compact` | 立即压缩，走带外流程，不会卡住聊天 |
| `/model` | `/model [名称\|default]` | 不带参数时列出已配置的模型；`/model <名称>` 把会话绑定到那个模型，`/model default` 解绑、回到默认模型。绑定会持久化，和 Web UI 的模型选择器看到的是同一个 |
| `/goal [...]` | | 和 Web 共用同一套实现——`/goal edit <文本>` 一步到位直接改 |
| `/stop` | `/stop` | 中断正在进行的轮次 |
| `/status` | `/status` | 报告这个聊天绑定了多久，以及输入/输出 token 数 |
| `/list` | `/list` | 列出最多 20 个已保存的会话，编号供 `/bind` 用 |

:::note[IM 怎么解析一条 `/` 消息]
保留命令（上表）由命令 router 处理，不会被同名 skill 抢走。`/loop` 和任何匹配到已发现 skill 的
`/<skill-name>` 会作为普通文本透传给模型，和 TUI、Web 的行为一致。只有两边都匹配不上的斜杠词
才会返回 "Unknown command"。
:::

`/bind`、`/unbind`、`/clear`、`/new` 都会清掉这个聊天记住的权限选择缓存和记忆注入器状态，
因为这些东西是绑定在"即将被替换掉"的那个对话上的——见[权限系统](/docs/zh/reference/permissions/)。

## 一览表

| 命令 | TUI | Web | IM |
|---|:-:|:-:|:-:|
| `/help` | ✓ | | |
| `/model` | ✓ | | ✓ |
| `/thinking` | ✓ | | |
| `/compact` | ✓ | ✓ | ✓ |
| `/clear` | ✓ | ✓ | ✓ |
| `/goal ...` | ✓ | ✓ | ✓ |
| `/skills` `/mcp` `/workflows` `/memory` `/save` `/sessions` `/init` | ✓ | | |
| `/exit` `/quit` | ✓ | | |
| `/<skill-name>` | ✓ | ✓（通过选择菜单） | |
| `/bind` `/unbind` `/new` `/stop` `/status` `/list` | | | ✓ |

下一步：会话绑定与重绑定的更多细节见[接入聊天应用](/docs/zh/guides/channels/)。
