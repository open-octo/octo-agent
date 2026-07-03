---
title: Bridge to chat apps
description: Delegate tasks to octo from WeChat, Feishu, DingTalk, WeCom, Discord, or Telegram.
---

The IM bridge runs inside `octo serve` — no separate process. Add octo to a chat and delegate tasks
from your phone without opening a terminal.

```bash
octo serve
```

## Supported platforms

| Platform | Setup |
|---|---|
| WeChat (iLink) | scan-to-login QR in the web UI's **Channels** panel |
| Feishu | app credentials in the **Channels** panel |
| DingTalk | app credentials in the **Channels** panel |
| WeCom | app credentials in the **Channels** panel |
| Discord | bot token in the **Channels** panel |
| Telegram | bot token in the **Channels** panel |

Each adapter is configured, tested, and can send a one-off message straight from the panel — there's
a **Send test message** action per platform before you rely on it.

## What you get per channel

- Per-user sessions — each chat participant gets their own conversation history and permission
  context, not a shared one.
- Slash commands work the same as the TUI (`/compact`, `/clear`, `/skills`, …).
- Attachments bridge both ways: images sent to octo become vision blocks, documents get a
  `read_file` note; octo can send files back with the `send_file` tool.
- Interactive permission prompts arrive as a chat message — your next reply answers it, no separate
  approval channel to check.

Next: pair channels with [goals](/docs/guides/goals/) for tasks that keep running between messages,
or read the full surface in the [HTTP & SSE API reference](/docs/reference/http-api/).
