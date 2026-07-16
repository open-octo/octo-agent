---
title: 安全模型
description: 安全边界划在哪里，靠什么保护，哪些是有意不管的。
---

octo 是一个**单用户、私人使用的 agent**。它的服务（`octo serve`）代表它唯一受信任的操作者执行任意
shell 命令——这是产品设计本身，不是一个漏洞。这一页说明安全边界划在哪里、靠什么保护，
以及哪些是有意留在边界之外的。

## 边界在哪

- **只有一个用户。** 没有账号，没有角色。持有访问密钥的人（或者坐在这台机器前的人）拥有完全控制权，
  包括通过聊天执行命令。
- **回环地址是被信任的。** 来自 `127.0.0.1`/`::1` 的请求不需要 key 认证——反正任何已经能在这台机器上
  以本地用户身份跑代码的东西，本来就在边界之外了。
- **其他所有地方都需要 key。** `octo serve` 默认绑定 `127.0.0.1:8088`；绑得更宽
  （`-addr :8088`）之后，每一个来自非回环客户端的 API 和 WebSocket 请求都需要
  [访问密钥](#访问密钥)。

## 防住了什么

| 威胁 | 防御手段 |
|---|---|
| 局域网/公网攻击者调用暴露出去的 API | 默认只绑回环地址；非回环请求都要过 256 位访问密钥（常数时间比较） |
| 恶意网站从你自己的浏览器对 `http://localhost:8088` 发起 CSRF | `Origin` 必须是本地或者在 `--cors` 白名单里（字面量 `*` 永远不会被接受）；认证 cookie 是 `SameSite=Strict` |
| DNS rebinding（攻击者的域名解析到 127.0.0.1） | 回环豁免要求 `Host` 头必须是本地地址 |
| 伪造客户端 IP | 回环豁免判断时完全不看 `X-Forwarded-For` |
| 上传文件的 XSSI 读取 | 提供的上传文件带 `X-Content-Type-Options: nosniff` |
| agent 用破坏性命令 wipe 掉整个系统 | 权限引擎硬编码 `deny`：`rm -rf /`、`dd`、`mkfs`、`fdisk`、`shutdown`、`reboot` 等，全平台覆盖；Windows 特有拒绝包括 `diskpart`、`format`、`bcdedit`、`reg delete`、`vssadmin delete`、`wbadmin delete`、`dism`、`rmdir /s /q`、`del /s /q` 及其 PowerShell 别名变体（`ri`、`del`、`erase` 带 `-r -fo`）；这些规则无法被 `~/.octo/permissions.yml` 覆盖 |
| agent 写入系统目录 | `write_file`/`edit_file` 硬编码拒绝 `/bin`、`/sbin`、`/usr`、`/System`、`/boot`、`/lib`、`/Windows`、`C:/Windows`、`C:/Windows/System32`、`C:/Windows/SysWOW64`、`C:/ProgramData` 等 Unix/macOS/Windows 系统目录 |
| agent 外泄数据或开启反弹 shell | 默认 `ask`：`curl`、`wget`、`ssh`、`scp`、`nc`、`socat`、`nmap`、`systemctl`、`iptables`、`crontab` 等 |
| agent 通过 Windows 管理工具破坏系统配置 | 默认 `ask`：`wmic`、`sc`、`schtasks`、`netsh`、`icacls`、`takeown`、`robocopy`、`xcopy`、`mklink`、`powershell`、`cmd /c` |
| 事后追查 | 只追加的 JSON 审计日志 `~/.octo/audit.log`，记录每一次 deny、ask-denied 和 user-allowed 决策 |

IM 渠道（飞书、钉钉、Discord 等）走各自平台的 bot 凭证加上 octo 的聊天/用户绑定单独认证；
这些适配器只发起出站连接，不暴露任何入站 HTTP 路由。

## 权限引擎

octo 对每一次工具调用都会经过规则驱动的权限引擎。默认规则内嵌在二进制中，可以由
`~/.octo/permissions.yml` 补充或调整。用户规则可以放宽或收紧策略，但**硬编码的 OS 级毁灭保护
不可覆盖**：`rm -rf /`、`dd if=/dev/zero of=/dev/sda`、`mkfs`、`fdisk`、`shutdown`、`reboot`、
`kill -9 -1` 等命令始终被拒绝，写入系统目录也始终被阻止。这防止了配置错误的权限文件
（或被 LLM 诱导改坏的文件）悄悄关闭护栏。

引擎有三种模式：
- `interactive`（CLI 默认）：ask 类决策会提示用户确认。
- `auto`：ask 类决策自动放行——方便，但需谨慎使用。
- `strict`：ask 类决策直接拒绝，不提示——最适合无人值守 / cron / IM 场景。

## 审计日志

每一次非 `allow` 的权限决策都会以单行 JSON 追加到 `~/.octo/audit.log`：

```json
{"ts":"2026-07-16T14:12:00.000000000Z","tool":"terminal","input":{"command":"rm -rf /"},"decision":"deny","reason":"permission_denied: terminal matched deny rule (pattern: \"rm -rf /\"). This operation is blocked by policy."}
```

记录的类型包括 `deny`、`ask-denied`（非交互模式下 ask 被直接拒绝）、`user-declined`（用户拒绝）
和 `user-allowed`（用户点击确认放行）。日志文件权限为 `0600`，octo 不会截断它；用户可自行轮转或归档。

## 有意不防的东西

- **本机的恶意进程。** 回环流量是被信任的；任何以本地用户身份在同一台机器上运行的恶意软件都能
  访问这个 API。如果机器是共享的或者已经被攻破，访问密钥帮不上忙。
- **明文传输。** key 通过 HTTP 的 cookie/header 传输。在不受信任的网络上，请在前面接一层 TLS
  （反向代理、`tailscale serve`）或者用隧道。
- **暴力破解。** 没有锁定机制也没有限流——key 是 256 位 `crypto/rand` 生成的，在线猜测本来就不可行。

## 访问密钥

解析优先级：`octo serve --access-key` flag → `OCTO_ACCESS_KEY` 环境变量 →
`~/.octo/config.yml` 里的 `access_key` → 首次启动时自动生成并持久化（权限 `0600`）。
当绑定地址不是纯回环时，启动会打印一个带 key 的、可以直接打开的 URL；Web UI 会把它存起来，
并从地址栏里去掉。

客户端可以用 `Authorization: Bearer <key>`、`X-Access-Key`，或者 `octo_access_key` cookie 来
提供 key（`access_key` 查询参数只在 `/ws` 上生效）。`GET /api/health` 和 `GET /api/version`
是仅有的两个免认证路由，且不携带任何机密信息。

要轮换 key：编辑 `config.yml` 里的 `access_key` 并重启（或者调用 `POST /api/restart`）。

## 更新渠道

`octo upgrade`（以及 Web UI 的升级按钮）会校验下载的压缩包的 SHA-256，比对同一个 release 的
`checksums.txt`，两者都通过 GitHub 的 TLS 获取。这能防住传输损坏和镜像篡改——但**防不住**
一个被攻破的 GitHub 账号发布恶意 release；目前还没有签名层。版本*检查*（Web 徽标背后那个）是
自动的；*安装*从来不是自动的，并且和其他所有会修改状态的接口一样,要过访问密钥认证。

## 报告安全漏洞

请提交一个 [GitHub 安全公告](https://github.com/open-octo/octo-agent/security/advisories/new)，
或者私下邮件联系维护者——可利用的漏洞请不要发公开 issue。
