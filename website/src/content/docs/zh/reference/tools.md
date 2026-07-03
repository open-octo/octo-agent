---
title: 工具参考
description: octo 给模型的每一个内置工具。
---

内置工具默认全部开启（`--no-tools` 会关闭全部,包括 MCP 和 skill 执行）。每次调用都要先过一遍权限引擎。

## 文件系统与搜索

| 工具 | 作用 |
|---|---|
| `read_file` | 读取文件，可指定行范围 |
| `write_file` | 创建或覆盖一个文件 |
| `edit_file` | 做一次定向的查找/替换编辑 |
| `glob` | 按模式查找文件 |
| `grep` | 搜索文件内容 |

## Shell

| 工具 | 作用 |
|---|---|
| `terminal` | 运行一条 shell 命令（前台或后台） |
| `terminal_output` | 读取某个后台命令的输出 |
| `terminal_input` | 向后台命令的 stdin 写入内容（仅 POSIX 可靠，见[兼容性](/docs/zh/reference/compatibility/)） |
| `kill_shell` | 停止一个后台命令 |

要不要放后台是每次调用显式指定的，不是靠猜：`detached`（一个不被追踪的守护进程，比 octo
自己活得还久）、`run_in_background: "async"`（被追踪、一次性——完成后自动推送结果，
`terminal_output`/`terminal_input` 对它不适用）、`run_in_background: "interactive"`
（被追踪且长期运行，这两个工具都能用来读写它）。不放后台的命令同步执行，超时 120 秒。

其实每一条同步命令底层都是先当一个隐藏的后台进程启动的，这样可以提前把它转正，而不是干等超时——
在 TUI 里，`Ctrl+B` 会把当前正在跑的同步命令（子代理自己跑的 terminal 调用也算）转成一个可见、
可追踪的后台进程；Web UI 有个按钮效果一样。IM 里没有转正的入口——只有超时会生效。

每个后台进程的输出上限是 stdout+stderr 合计 1MiB，超出部分先丢最老的字节；同时能跑多少个后台进程
没有上限；宿主进程关闭时，所有被追踪的进程（`detached` 的除外）都会被杀掉。

## Web

| 工具 | 作用 |
|---|---|
| `web_fetch` | 抓取并读取一个 URL |
| `web_search` | 搜索网页 |
| `browser` | 通过 CDP 操作一个真实 Chrome 标签页——见[浏览器自动化](/docs/zh/guides/browser-automation/) |

## Agent 与编排

| 工具 | 作用 |
|---|---|
| `sub_agent` | 启动一个子代理（同步或异步） |
| `sub_agent_send` / `sub_agent_status` / `sub_agent_kill` | 跟进、轮询或停止一个异步子代理 |
| `workflow` | 运行一段确定性的多 agent 编排脚本 |
| `workflow_status` / `workflow_kill` | 轮询或停止一个后台工作流 |
| `workflow_save` | 把一段脚本保存成一个有名字、可复用的工作流 |
| `task_create` / `task_update` / `task_list` | 跟踪一项更大工作里的具体步骤 |
| `schedule_wakeup` | 请求延迟一段时间后被唤醒（`/loop` 这类周期性工作靠它） |

## 目标

| 工具 | 作用 |
|---|---|
| `get_goal` / `create_goal` / `update_goal` | 读取、开始或修改会话的长期目标——见[运行长周期目标](/docs/zh/guides/goals/) |

## Skill 与 MCP

| 工具 | 作用 |
|---|---|
| `skill` | 按需加载某个 skill 的完整指令 |
| `mcp_search` / `mcp_describe` / `mcp_call` | 为延迟加载的 MCP schema 提供的 Tool Search 桥接——见[接入 MCP 服务](/docs/zh/guides/connect-mcp-servers/) |

当 Tool Search 关闭（或还没激活）时，每个已连接 MCP 服务自己的工具也会直接以
`mcp__<server>__<tool>` 的形式出现。

## 交互与其他

| 工具 | 作用 |
|---|---|
| `ask_user_question` | 在轮次进行中向用户提一个澄清性问题 |
| `send_message` | 在当前渠道发一条消息，但不结束这一轮 |
| `send_file` | 把文件发回去（IM 渠道） |
| `show_artifact` | 在 Web UI 的 artifact 面板里展示一个构建好的 HTML/Markdown/图片文件 |
| `restart_server` | 请求一次服务器[重启](/docs/zh/guides/self-host/#重启)（比如改完配置之后）；始终是 `ask` 档位，不可能被加进白名单 |

下一步：工具调用是怎么被门控的，见 [Agent 循环](/docs/zh/concepts/agent-loop/)。
