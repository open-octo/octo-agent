---
title: 接入聊天应用
description: 从微信、飞书、钉钉、企微、Discord 或 Telegram 给 octo 派活。
---

IM 桥接跑在 `octo serve` 内部——不需要额外起一个进程。把 octo 加进聊天，就能不打开终端从手机上派活。

```bash
octo serve
```

## 支持的平台

| 平台 | 接入方式 |
|---|---|
| 微信（iLink） | 在 Web UI 的 **Channels** 面板里扫码登录 |
| 飞书 | 在 **Channels** 面板里填应用凭证 |
| 钉钉 | 在 **Channels** 面板里填应用凭证 |
| 企微 | 在 **Channels** 面板里填应用凭证 |
| Discord | 在 **Channels** 面板里填 bot token |
| Telegram | 在 **Channels** 面板里填 bot token |

每个渠道都能在面板里直接配置、测试，并且有一个**发送测试消息**的操作，让你在真正依赖它之前先验证一下。

## 每个渠道能拿到什么

- 按用户分会话——每个聊天参与者都有自己独立的对话历史和权限上下文，不是共用一个。
- Slash 命令和 TUI 里一致（`/compact`、`/clear`、`/skills` 等）。
- 附件双向桥接：发给 octo 的图片会变成 vision block，文档会生成一条 `read_file` 记录；
  octo 也可以用 `send_file` 工具把文件发回来。
- 交互式权限确认会以一条聊天消息的形式出现——你的下一条回复就是答案，不需要另外找一个审批入口。

下一步：把渠道和[目标](/docs/zh/guides/goals/)搭配，让任务在消息之间持续推进；
完整能力面见 [HTTP 与 SSE API 参考](/docs/zh/reference/http-api/)。
