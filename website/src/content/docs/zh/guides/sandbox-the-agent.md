---
title: 沙箱化运行
description: 由操作系统强制执行的 terminal 工具限制。
---

`--sandbox` 会把 `terminal` 工具限制在项目目录加临时目录、禁止联网，由操作系统强制执行——
macOS Seatbelt、Linux Landlock + seccomp。默认关闭，并且当操作系统机制不可用时会 **fail closed**
（直接拒绝运行），而不是悄悄跑在不受限的环境里。

```bash
octo --sandbox                              # 限制目录，禁网络
octo --sandbox --sandbox-allow-net          # 允许联网
octo --sandbox --sandbox-write ./build      # 额外的可写目录（可重复）
octo --sandbox --sandbox-read /opt/data     # 额外的只读目录（可重复）
```

## 平台支持

Linux 和 macOS 是一等公民。**`--sandbox` 在 Windows 上不可用**——操作系统级限制只支持
Seatbelt/Landlock，所以在 Windows 上 `--sandbox` 会直接拒绝运行，而不是假装限制了什么。
权限引擎（每次工具调用前的交互式确认）是那边唯一的安全层。

下一步：把沙箱和[hooks](/docs/zh/guides/hooks/)搭配使用可以实现全自动、依然受限的循环；
完整的安全边界见[安全模型](/docs/zh/reference/security/)。
