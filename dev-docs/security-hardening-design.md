# 安全防护体系

octo 是一个单用户、本地的 Agent 系统，**执行 shell 命令是产品能力，不是漏洞**。但正因为 agent 可以在用户机器上做任何事，安全防护是核心基础设施。本文从开发者视角梳理整个安全体系：架构分层、关键设计决策、各层之间的协作关系。

## 架构一览

```
用户消息 → Agent Loop → Tool Call
                              │
                              ▼
                   ┌─────────────────────┐
                   │  PermissionGate      │  ← 多路复用：CLI/Web/IM 共用一个 gate
                   │  (internal/app)      │
                   └────────┬────────────┘
                            │
                            ▼
                   ┌─────────────────────┐
                   │  Permission Engine   │  ← 规则引擎
                   │  (internal/permission)│
                   │                      │
                   │  ┌─ defaults.yml    │  ← 内嵌默认规则（优先级低）
                   │  ├─ permissions.yml │  ← 用户覆盖规则（中优先级）
                   │  └─ hardcoded denies│  ← 引擎级硬编码（最高优先级）
                   └────────┬────────────┘
                            │
                    ┌───────┴───────┐
                    │               │
                    ▼               ▼
            ┌──────────┐    ┌──────────┐
            │ Audit Log │    │ Remember │
            │ (append)  │    │  (会话)  │
            └──────────┘    └──────────┘
```

## 三层规则体系

规则的优先级是 **deny > ask > allow**，不依赖于声明顺序。三层规则按以下顺序叠加：

### 第 1 层：硬编码引擎级规则（`applyHardcodedDenyRules`）

位置：`internal/permission/permission.go` 的 `New()` 方法末尾

这是最后防线：即使 `~/.octo/permissions.yml` 写了 `allow: { pattern: "" }`（全面放行），以下操作仍然被拒绝：

**跨平台毁灭操作：**
- `dd if=` — 磁盘原始写入
- `mkfs`、`format` — 创建/格式化文件系统
- `fdisk`、`parted`、`diskpart` — 分区操作
- `shutdown`、`poweroff`、`halt`、`init 0`、`reboot`、`systemctl poweroff/reboot` — 关机/重启
- `kill -9 -1`、`kill -SIGKILL -1` — 杀死所有进程
- `rm -rf /`、`rm -rf ~` — 根/家目录删除

**macOS 专用：**
- `diskutil erase`、`diskutil partition` — 抹盘/分区

**Windows 专用：**
- `reg delete` — 注册表删除
- `bcdedit` — 启动配置修改
- `wbadmin delete` — 备份目录删除
- `dism` — 系统镜像修改
- `vssadmin delete` — 卷影副本删除
- `rmdir /s /q`、`rd /s /q` — 递归静默删除
- `del /s /q`、`erase /s /q` — 批量静默删除
- `wevtutil cl` — 事件日志清空
- `fsutil` — 文件系统工具
- `sdelete` — 安全擦除

**系统目录文件写入保护（`write_file`/`edit_file`）：**
- Unix: `/bin/**`、`/sbin/**`、`/usr/bin/**`、`/usr/sbin/**`、`/boot/**`、`/lib/**`
- macOS: `/System/**`
- Windows: `C:/Windows/**`、`C:/Windows/System32/**`、`C:/Windows/SysWOW64/**`、`C:/ProgramData/**`
- 跨平台: `/Program Files/**`、`/Windows/**`

> 设计原则：硬编码规则只覆盖**可明确判定为灾难性的操作**（执行后系统无法继续运行或数据不可恢复）。通用管理工具（`wmic`、`powershell`、`sc` 等）虽然有风险但合法用途更多，放到第 2 层 ask。

### 第 2 层：默认规则（`defaults.yml`）

位置：`internal/permission/defaults.yml`，通过 `//go:embed` 嵌入二进制

这是出厂自带的默认策略，涵盖常见的危险/安全操作：

**硬拒绝（deny）**：同第 1 层，但在 YAML 中也声明了一份。实际裁决时同一条目在两层都有，但因为有第 1 层兜底，此处主要是文档化和方便测试。

**交互确认（ask）**——用户需点击确认才能执行：
- 文件删除：`rm -rf`、`rm -fr`
- Git 危险操作：`git push --force`、`git push -f`
- 提权：`sudo`、`chmod -R 777`
- 网络外泄：`curl`、`wget`、`ssh`、`scp`、`nc`、`ncat`、`socat`、`nmap`
- 系统管理：`systemctl`、`iptables`、`ip6tables`、`pfctl`、`crontab`、`launchctl`、`diskutil`
- 容器破坏：`docker rm -f`、`docker rmi -f`、`docker system prune`
- 磁盘耗尽：`fallocate -l`、`truncate -s`
- Windows 管理：`wmic`、`sc`、`schtasks`、`netsh`、`icacls`、`takeown`、`robocopy`、`xcopy`、`mklink`、`powershell`、`cmd /c`
- PowerShell 危险命令：`Invoke-Expression`、`Invoke-WebRequest`、`Invoke-RestMethod`、`Set-ExecutionPolicy`、`Start-Process`
- 敏感路径访问：`/etc/shadow`、`/etc/passwd`、`.ssh/id_`、`.aws/credentials`、`id_rsa`、`id_ed25519` 等

**自动放行（allow）**——不影响用户：
- 常用命令：`ls`、`cat`、`echo`、`pwd`
- Git 查询：`git status`、`git log`、`git diff`、`git branch`
- Go 工具链：`go test`、`go build`、`go vet`、`gofmt`
- 控制面工具：`sub_agent`、`skill`、`ask_user_question`、`task_*` 等

**文件读写路径拒绝：**
- `write_file`/`edit_file`：`.ssh/**`、`/etc/**`、`.env`、`.env.*`、`id_rsa*`、系统目录（重复但做双保险）
- `read_file`：`.ssh/id_*`、`id_rsa*`、`id_ed25519*`、`.env`、`.env.*`

**web_fetch SSRF 保护：**
拒绝所有私有/环路/链路本地地址范围。

### 第 3 层：用户覆盖规则（`~/.octo/permissions.yml`）

用户可以创建此文件来覆盖任意工具的全部规则。覆盖是**全量替换**（per-tool merge, not append）——用户为 `terminal` 写的规则会完全替换 `defaults.yml` 中的 terminal 规则，但**第 1 层硬编码规则不受影响**。

缓存机制：如果用户编辑文件导致语法错误，引擎会回退到最后一次加载成功的规则，保证 `octo serve` 不会因为一个未保存完的编辑而拒绝所有工具调用。

## 三种模式（Mode）

定义了 ask 决策如何被解析：

| Mode | ask 的解析 | 适用场景 |
|------|-----------|---------|
| `interactive`（默认） | 提示用户确认 | CLI/TUI 交互式使用 |
| `auto` | ask → allow | 信任环境、测试、无头运行 |
| `strict` | ask → deny | 无人值守、cron 任务、IM 静默频道 |

模式可以在 `~/.octo/config.yml` 中设置 `permission_mode`，CLI 可以 `--permission-mode` 覆盖，Web/TUI 有运行时切换入口。

## 认证与传输安全

### Access Key

API 访问密钥，解析顺序：`--access-key` 参数 → `OCTO_ACCESS_KEY` 环境变量 → `config.yml` → 自动生成。默认绑定 `127.0.0.1:8088`（仅回环），绑更宽时需要 key。

展示方式：`Authorization: Bearer <key>`、`X-Access-Key` header、`octo_access_key` cookie。`GET /api/health` 和 `GET /api/version` 是仅有的免认证接口。

### Web 端 CSRF/XSS 防护

- CORS：`Origin` 必须是本地或 `--cors` 白名单中的域名；字面量 `*` 永不反射
- auth cookie 是 `SameSite=Strict`
- Markdown 渲染经 `dompurify` 消毒 + `marked` 解析；链接仅允许 `http/https/mailto/tel`
- Artifact iframe 默认无 `allow-scripts`，需用户逐文件授权
- 上传的 `.html`/`.htm`/`.js`/`.mjs` 强制 `Content-Disposition: attachment`

### Web Hook 防护

见 `internal/mcp/` 的 MCP 命令白名单：
- stdio MCP 命令必须是简单 basename（无路径分隔符）
- 白名单：`npx`、`npm`、`node`、`uvx`、`uv`、`python`、`python3`、`cargo`、`go`、`ruby`
- 非白名单命令需用户显式勾选 `allow_arbitrary_command`

## 审计日志（Audit Log）

位置：`internal/audit/`，输出到 `~/.octo/audit.log`

每一次非 `allow` 的权限决策都会追加为一行 JSON：

```json
{"ts":"2026-07-16T14:12:00.000000000Z","tool":"terminal","input":{"command":"rm -rf /"},"decision":"deny","reason":"permission_denied: terminal matched deny rule..."}
```

记录类型：`deny`、`ask-denied`、`user-declined`、`user-allowed`

设计要点：
- 追加写入，octo 从不截断；用户自行轮转或归档
- 文件权限 `0600`
- 写入失败不会阻塞工具调用（slog.Warn 后继续）

## 会话记忆（Remember）

`permission.Remembered` 是跨引擎构建周期的持久决策缓存。用户在交互提示中点了"Always allow"后，该 (tool, input) 签名在当前会话中不再提示。

关键约束：
- deny 规则始终击败缓存——配置变更后新加的 deny 会覆盖之前的 remember
- `write_file`/`edit_file` 按 path 缓存（不是按完整 input），所以一次确认允许编辑同一文件的不同内容
- 每个会话有自己的 Remembered 存储，会话清理时一并释放

## Web Fetch SSRF 保护

拒绝列表：
- IPv4 私有范围：`10.*`、`192.168.*`、`172.16-31.*`
- IPv4 回环：`127.*`
- IPv6 回环：`::1`、`0:0:0:0:0:0:0:1`
- IPv4-mapped IPv6 回环：`::ffff:127.*`、`::ffff:7f00:*`
- 链路本地：`fe80:*`
- 唯一本地地址：`fc*:*`、`fd*:*`
- 链接本地：`169.254.*`、`localhost`、`*.local`

白名单（公开 API）：`github.com`、`stackoverflow.com`、`go.dev`、`pkg.go.dev`

## 关键设计决策

1. **规则三条腿（deny/ask/allow）+ 三层叠加**：不是 positional first-match。这允许 safely compose 不同来源的规则——用户无法意外用 allow 覆盖一个 deny。
2. **硬编码规则不可覆盖**：牺牲了用户的绝对配置自由度来换安全底线。Agent 是个人的编码助手，不是全沙箱 OS——防止灾难性数据丢失比让用户（或 LLM）关闭护栏更重要。
3. **审计日志 best-effort 写入**：不阻塞工具调用。写入失败只记录 warn，agent 继续执行。这是权衡——审计的目的是事后追溯，不是实时控制。
4. **`patternMatches` 的边界锚定**：`rm -rf /` 根路径删除用 `/` 结尾锚定，不误伤 `rm -rf /Users/me/project`。`allowPatternMatches` 严格前缀匹配 + 禁止 shell 链式字符，阻止 `ls && ./pwn`。
5. **路径规则跨平台一致**：`absPath` 将 `\` 统一转为 `/`，路径 glob 规则在 Unix/Windows 行为一致。
6. **PowerShell 命令 case-sensitive 子串匹配**：PowerShell 是 case-insensitive 且有大量别名，用 canonical 形式覆盖 LLM 最常生成的命令；漏掉的由隐式 ask 兜底（交互模式提示，strict 模式拒绝），不会自动执行。
