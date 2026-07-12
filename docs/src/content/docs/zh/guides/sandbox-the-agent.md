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

## 回收站

沙箱管住的是 agent 伸到项目*外面*的手，回收站则是项目*里面*的安全网：agent 发疯删错、覆盖错
文件，不该让你丢掉工作成果。octo 在 `~/.octo/trash/` 维护一个按项目隔离的文件级回收站。

- **删除被拦截。** Agent 发起的 `rm` / `del` / `Remove-Item` 会被包一层，目标在删除*之前*先复制进
  回收站。Agent 通过 session、skill、workflow、调度器、记忆等途径的删除也同样受保护。
- **覆盖会备份。** `write_file` / `edit_file` 覆盖已有文件前，旧内容先暂存进回收站，工具结果里给出
  一键 **撤销**。已被 git 跟踪且干净的文件会跳过——`git checkout` 本就能还原，回收站因此保持精简。
- **还原绝不覆盖。** 如果原路径处已重新存在文件，还原不会静默覆盖它：octo 会中止、还原到旁边、或先
  把当前文件移入回收站——由你选择。
- **记录来源。** 每条记录都知道是谁删的（`rm`、`write_file`、某个 session 及其标题、某个 skill、某个
  workflow……）以及何时删的。
- **自动有界。** 条目 14 天后自动过期，回收站上限 10 GiB，超限时按最旧优先淘汰。二者都可在
  `config.yml` 里配置：

```yaml
trash:
  retention_days: 14   # 0 = 默认（14）；负数则禁用过期
  max_size_mb: 10240   # 0 = 默认（10 GiB）；负数则禁用上限
  overwrite_backup: true  # write_file/edit_file 覆盖前是否备份
```

从 Web UI 的 **文件回收站** 面板（`octo serve`）还原、撤销、清空回收站。

下一步：把沙箱和[hooks](/docs/zh/guides/hooks/)搭配使用可以实现全自动、依然受限的循环；
完整的安全边界见[安全模型](/docs/zh/reference/security/)。
