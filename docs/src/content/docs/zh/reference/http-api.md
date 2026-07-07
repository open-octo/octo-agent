---
title: HTTP API
description: octo serve 背后的 REST 接口面——和内置 Web UI 用的是同一套。
---

`octo serve` 暴露一套统一的 REST + WebSocket API；内置的 Web UI 只是它的第一个客户端。
下面所有路由都以 `/api` 开头，任何非回环绑定下都需要[访问密钥](/docs/zh/reference/security/)。
`GET /api/health` 和 `GET /api/version` 是仅有的两个不需要认证的路由。

## 对话与流式输出

| 路由 | 作用 |
|---|---|
| `POST /api/chat` | 创建一个对话 |
| `POST /api/chat/{id}/turn` | 发送一轮 |
| `GET /ws` | WebSocket —— Web UI 用的实时会话/任务/工作流事件 |

## 会话

| 路由 | 作用 |
|---|---|
| `GET/POST /api/sessions` | 列出 / 创建 |
| `GET /api/sessions/{id}` | 获取单个，或它的 `/messages`、`/artifacts` |
| `DELETE`, `PATCH /api/sessions/{id}` | 删除，或更新（model、推理强度、是否显示推理、权限模式、工作目录） |
| `GET/PUT/DELETE /api/sessions/{id}/goal` | 读取、设置或清除会话的目标 |

## 工具、Skill、工作流

| 路由 | 作用 |
|---|---|
| `GET /api/tools` | 列出可用工具 |
| `GET /api/skills`, `PATCH /api/skills/{name}/toggle`, `DELETE`, `POST /api/skills/import`, `GET .../export` | 管理 skill |
| `GET /api/workflows` | 列出工作流 |
| `GET/POST/PATCH/DELETE /api/mcp/servers` | 管理 MCP 服务 |

## Channels（IM 桥接）

| 路由 | 作用 |
|---|---|
| `GET /api/channels`, `/available` | 列出已配置 / 可接入的平台 |
| `GET/POST/DELETE /api/channels/{platform}` | 读取、保存或移除某个平台的配置 |
| `POST /api/channels/{platform}/test` | 发一条测试消息 |
| `POST /api/channels/{platform}/send`, `/send-file` | 通过 API 发送 |
| `POST/GET/DELETE /api/channels/weixin/login` | 微信扫码登录流程 |

## 任务、Profile、记忆、回收站

| 路由 | 作用 |
|---|---|
| `GET/POST/PATCH/DELETE /api/tasks` | 任务图（`task_create`/`task_update`/`task_list` 在 HTTP 上的镜像） |
| `GET /api/profile/soul`, `/api/profile/user` | 读取 `soul.md` / `user.md` |
| `GET /api/memories` | 读取记忆索引 |
| `GET /api/trash`, `POST .../empty`, `POST .../{id}/restore`, `DELETE /api/trash/{id}` | 可恢复删除的回收站面板 |

## 引导流程、配置、Provider

| 路由 | 作用 |
|---|---|
| `GET/POST /api/onboard/status`, `/complete` | 首次运行的引导状态 |
| `GET /api/providers`, `GET /api/config` | 可用的 provider；生效的配置 |
| `POST /api/config/test` | 验证一组 provider/model/key 组合是否可用 |
| `POST/PATCH/DELETE /api/config/models{,/{id}}` | 管理 model 条目；`/default`、`/lite` 设置两个特殊槽位 |

## 浏览器与上传

| 路由 | 作用 |
|---|---|
| `GET /api/browser/status`, `POST /api/browser/verify` | CDP 连接状态 |
| `GET/PUT/DELETE /api/browser/recordings{,/{name}}` | 管理录制/回放 |
| `POST /api/upload`, `GET /api/uploads/{name}` | 聊天附件用的文件上传 |

## 服务生命周期

| 路由 | 作用 |
|---|---|
| `POST /api/restart` | 请求一次[重启](/docs/zh/guides/self-host/#重启)；立即返回 `202`，排空正在进行的轮次的过程在后台跑 |

这套 API 属于 **Best-effort**（见[兼容性](/docs/zh/reference/compatibility/)）：用 `curl`
调用是文档化、受支持的行为，但路由和字段可能在某个小版本里变化，并在发布说明里说明——
目前还没有带版本号的 `/api/v1`。

下一步：[安全模型](/docs/zh/reference/security/)详细说明了访问密钥到底防住了什么、
哪些地方不需要它（回环地址）。
