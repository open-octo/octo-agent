---
title: 定时任务
description: 按计划自动触发的 agent prompt，由 octo serve 负责跑。
---

cron 任务是一个按计划自动触发的 agent prompt——"每 5 分钟查一次队列"、"每天早上 9 点写一份
日报"——只要 `octo serve` 在跑，没人盯着它也会自己触发。

```bash
octo serve
```

## 工作原理

每个任务是 `~/.octo/tasks/` 下的一个 JSON 文件，由 `octo serve` 内部的调度器加载。任务触发时，
调度器会用任务的 prompt 跑一轮 agent。每次运行都有一个 **30 分钟的墙钟超时**——唯一的硬上限。

**任务只有在 `octo serve` 跑着的时候才会触发。** 没有 serve 就没有运行——服务器关着期间错过
的那次计划，重启后也不会补跑。

## 任务字段

| 字段 | 是否必填 | 含义 |
|---|---|---|
| `name` | 是 | 人类可读的任务名 |
| `cron` | 是 | 调度表达式——见下文 |
| `prompt` | 是 | 每次运行发给 agent 的 prompt |
| `model` | 否 | 模型覆盖；不填就用 server 的默认模型 |
| `agent` | 否 | `"general"` 或 `"coding"` |
| `directory` | 否 | 运行时所在的工作目录 |
| `notify` | 否 | 每次运行的最终回复（或失败信息）要推送到哪些 IM 会话 |
| `enabled` | 是 | 这个计划当前是否处于启用状态 |

这个 prompt 是在自己独立的会话里跑的，拿不到创建这个任务的那个对话的任何上下文，所以它必须
自成一体：做什么、在哪做、输出该长什么样都要写清楚。也要给它一个明确的停止条件——一个开放式
的 prompt 会让模型一直反复确认下去，直到撞上 30 分钟超时，而不是"没什么可汇报的"就自己收尾。

## 会话与分组

每次运行都会**新建一个会话**，标题就是这次运行的本地日期和时间（如 `2026-07-22 15:04`），
从空白记录开始——各次运行之间从不共享会话。每个任务还会自动拥有一个以任务名命名的**会话分组**，
所有运行都归入其中，在侧边栏里聚成一堆。分组随任务创建，也随任务改名/删除而改名/删除（删除分组
只是解除归组，会话本身保留在磁盘上）。

## cron 表达式——6 个字段，秒在最前面

调度器用的是 [robfig/cron](https://github.com/robfig/cron)，**带一个秒字段**——标准的 5 字段
crontab 写法在这里是非法的，永远要在最前面加一个秒字段：

```
seconds minutes hours day-of-month month day-of-week
```

| 想要 | 表达式 |
|---|---|
| 每天 09:00 | `0 0 9 * * *` |
| 每 30 分钟 | `0 */30 * * * *` |
| 工作日 18:30 | `0 30 18 * * 1-5` |
| 每月 1 号 08:00 | `0 0 8 1 * *` |

也支持描述符写法：`@hourly`、`@daily`、`@weekly`、`@every 90m`。时间用的是 server 所在的
本地时区。

## 通过 API 管理任务

只要 `octo serve` 在跑，所有走 API 的改动都会立刻让正在运行的进程重新调度——这是推荐路径。

```bash
# 创建——返回 {"id":"task_..."}。任何可选字段（directory、model、
# agent、notify）都可以直接放进创建时的请求体里。
curl -s -X POST http://127.0.0.1:8088/api/tasks \
  -H 'Content-Type: application/json' \
  -d '{"name":"daily-report","cron":"0 0 9 * * *","prompt":"Summarize ...","directory":"/srv/repo"}'

curl -s http://127.0.0.1:8088/api/tasks                # 列表
curl -s -X DELETE http://127.0.0.1:8088/api/tasks/{id} # 删除

# 立即跑一次，跳出计划
curl -s -X POST http://127.0.0.1:8088/api/tasks/{id}/run

# 改任意一部分字段——启用/禁用也是走这条路
curl -s -X PATCH http://127.0.0.1:8088/api/tasks/{id} \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"new prompt ...","enabled":false}'
```

`PATCH /api/tasks/{id}` 接受 `name`、`enabled`、`cron`、`prompt`、`model`、`agent`、
`directory`、`notify`——只发你要改的那部分就行；用 `name` 改名也会同步重命名任务的会话分组。Web UI 里的调度器面板就是这套 API 的一个
客户端，所以用 `curl` 创建的任务会出现在面板里，反过来也一样；面板也是**试跑一个新任务的
`Run` 按钮**推荐用的地方，而不是从聊天会话里直接触发 `/api/tasks/{id}/run`——一次运行是在这
个任务**自己的**会话里跑一整轮 agent（最多 30 分钟），从对话里触发只会把那个对话卡住，而真
正的输出落在了没人看着的地方。

### 没有运行中的 server 时

直接写 `~/.octo/tasks/<id>.json`（`id` 格式是 `task_<unix-millis>`；文件名必须等于
`<id>.json`）：

```json
{
  "id": "task_1717999999999",
  "name": "daily-report",
  "cron": "0 0 9 * * *",
  "prompt": "Summarize ...",
  "directory": "/srv/repo",
  "enabled": true,
  "created_at": "2026-06-10T09:00:00Z"
}
```

这个文件会在 `octo serve` 下次启动时被读取。手写的文件如果 cron 表达式写错了，加载时会
静默失败（只会打到 stderr）。**server 已经在跑的时候，对文件的修改会被忽略，直到重启为止**——
一旦起来了，就应该改走 API。

## 通知

`notify` 是一个 IM 目标的列表（单个裸对象也接受）；每一条都会在运行成功后收到最终回复，
或者在出错时收到一条简短的失败提示。推送失败会记在 server 端日志里，但不会影响这次运行本身。

| 平台 | `chat_id` | 说明 |
|---|---|---|
| `feishu` | `oc_…` chat id | 需要在 `channels.yml` 里配好应用凭证；id 可以从会话设置里拿，或者给 bot 发条消息后去看 server 日志 |
| `dingtalk` | 单聊用 staff id，群聊用 `cid…` conversation id | 单聊的 conversation id 用不了——要用 staff id |
| `weixin`（iLink） | user id | 用户必须至少给 bot 发过一次消息 |
| `telegram` | chat id（用户/群组/频道） | bot 必须已经能给它发消息 |
| `discord` | channel id | bot 需要有那个频道的 Send Messages 权限 |
| `wecom` | 忽略 | 推送走的是绑定到某个群的群机器人 webhook，而不是这个字段 |

下一步：如果只是想要一个更短生命周期、不需要扛过重启的会话内重复任务，看看
[`/loop`](/docs/zh/guides/loop/)。
