# octo-agent

[![Go CI](https://img.shields.io/github/actions/workflow/status/open-octo/octo-agent/go.yml?label=ci&style=flat-square)](https://github.com/open-octo/octo-agent/actions)
[![Website](https://img.shields.io/badge/website-octo--agent.dev-4f46e5?style=flat-square)](https://octo-agent.dev)
[![Go](https://img.shields.io/badge/go-%3E%3D%201.25-00ADD8?style=flat-square)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-lightgrey?style=flat-square)](LICENSE.txt)

<p align="center">
  <a href="README.md">English</a> · <a href="README_CN.md">简体中文</a>
</p>

> **开源、单二进制、自托管的 AI Agent。** coding 能力对标 Claude Code，个人助手
> 比 OpenClaw 更轻量 —— 一个 MIT 开源的 Go 二进制，无需 Node / Python / Ruby，
> 接**任意模型**（DeepSeek、Kimi、Anthropic、OpenAI 或任何兼容端点），服务和
> 数据都留在你自己的机器上。

<!-- TODO(demo): 录一段 15–30s 首屏 GIF（装一行 → octo 接 DeepSeek → 解决一个真实编码任务），
     放到 landing/assets/demo.gif，然后取消下面这段注释。 -->
<!--
<p align="center">
  <img src="landing/assets/demo.gif" alt="octo 演示" width="760">
</p>
-->

```bash
curl -fsSL https://octo-agent.dev/install.sh | sh     # 单二进制，无需 Node / Ruby / Python 环境
octo config                                            # 选 provider，填 key（DeepSeek / Kimi / 百炼 …）
octo "给 octo config show 加一个 --json 参数并跑测试"   # 一句话 → 完整 agentic 工具循环
```

## 安装

- **Linux / macOS** — `curl -fsSL https://octo-agent.dev/install.sh | sh`
- **Windows** — `irm https://octo-agent.dev/install.ps1 | iex`
- **桌面应用** — 从[最新 release](https://github.com/open-octo/octo-agent/releases/latest)下载安装器：
  `octo-setup.pkg`（macOS）、`octo-setup.exe`（Windows）、`Octo-x86_64.AppImage`（Linux）
- **Go** — `go install github.com/open-octo/octo-agent/cmd/octo@latest`

随时用 `octo upgrade` 升级。各平台细节 —— Gatekeeper / SmartScreen 提示、卸载、
从源码构建 —— 见[安装指南](https://octo-agent.dev/docs/zh/getting-started/install/)。
Windows 安装器经 [SignPath Foundation](https://signpath.org/) 签名，完整的代码
签名政策见 [SECURITY.md](SECURITY.md#code-signing-policy)。

## 快速上手

```bash
octo config                # 一次性设置：选 provider/model，填 API key
octo "介绍一下这个仓库"      # headless 单发：prompt → agentic 工具循环 → 退出
octo                       # 终端交互式 TUI；octo -c 恢复历史 session
octo serve -d              # Web UI + IM 桥接，http://127.0.0.1:8088
```

内置工具（shell、文件读写改、搜索）、MCP 服务、skills 全部默认开启，一条消息
就能真正干活。下一步：
[快速上手](https://octo-agent.dev/docs/zh/getting-started/quickstart/) ·
[选择 provider](https://octo-agent.dev/docs/zh/getting-started/choose-a-provider/) ·
[CLI 参考](https://octo-agent.dev/docs/zh/reference/cli/)。

## 为什么用 octo

octo 不打算在功能上卷赢大厂 agent；它是同一个想法的**开源、可自托管、不绑厂商**
版本 —— 而且是有主张的那种，Rails 风格：约定优于配置，omakase 默认值优于无限选项。

|  | **octo-agent** | Claude Code |
|---|---|---|
| 授权 / 成本 | **MIT 开源，免费，自托管** | 专有，多数场景需 Claude 订阅 |
| 运行时 | **单个自包含 Go 二进制** | 原生安装，绑定 Anthropic 账户 |
| 模型 | **双协议原生 + 任意兼容端点**（DeepSeek/Kimi/百炼/OpenRouter/vLLM） | 以 Anthropic 为主 |
| 部署 / 数据 | **完全自托管，服务与数据都在你手里** | 多数场景由 Anthropic 托管 |
| 技能 | SKILL.md 格式相同，复用 Claude Code 技能 | 原生（skills 的发源地） |

在个人助手这条线上，[OpenClaw](https://github.com/openclaw/openclaw) 是最接近的
同类。octo 覆盖同一块地盘 —— 自托管、MIT、在你已经在用的聊天 App 里找到你 ——
但它是单个静态二进制而不是带依赖树的 Node.js 应用，且自带完整的 coding agent 核心。

## 界面

**稳定版（1.0）。** 规划了八个界面 —— 正好对上章鱼的八条腕 —— 其中七个已上线：

- **CLI** —— 终端里是交互式 TUI，其余场景是 headless 单发
- **Web UI** —— `octo serve`，基于 REST + WebSocket 的本地仪表盘
- **桌面应用** —— 原生窗口 + 系统托盘（macOS / Windows / Linux）
- **IM 桥接** —— 微信 iLink、飞书、钉钉、企微、Discord、Telegram，随 `octo serve` 运行
- **VS Code 插件** —— [`open-octo/octo-vscode`](https://github.com/open-octo/octo-vscode)
- **Obsidian 插件** —— [`open-octo/octo-obsidian`](https://github.com/open-octo/octo-obsidian)
- **Go SDK** —— [`pkg/octoagent`](pkg/octoagent)，把 agent 循环嵌进你自己的程序

第八个界面移动端 App 即将上线。哪些接口可以放心依赖见
[COMPATIBILITY.md](COMPATIBILITY.md)；安全边界见 [SECURITY.md](SECURITY.md)。

## 深入了解

完整文档在 **[octo-agent.dev/docs](https://octo-agent.dev/docs/zh/)**：

- [Skills](https://octo-agent.dev/docs/zh/guides/use-skills/) —— 兼容 Claude Code 的 SKILL.md；软链 `~/.claude/skills` 直接复用现有技能
- [沙箱与回收站](https://octo-agent.dev/docs/zh/guides/sandbox-the-agent/) —— OS 强制隔离（Seatbelt / Landlock），外加文件级回收站，agent 的删除和覆盖都先备份
- [MCP 服务](https://octo-agent.dev/docs/zh/guides/connect-mcp-servers/) —— stdio + HTTP、OAuth，以及面向大工具集的 Tool Search
- [记忆](https://octo-agent.dev/docs/zh/guides/memory/) · [子代理](https://octo-agent.dev/docs/zh/guides/sub-agents/) · [工作流](https://octo-agent.dev/docs/zh/guides/workflows/) —— 持久化与多代理编排
- [浏览器自动化](https://octo-agent.dev/docs/zh/guides/browser-automation/) —— CDP 录制 / 回放 / 自愈
- [IM 渠道](https://octo-agent.dev/docs/zh/guides/channels/) —— 把 octo 接进你的聊天 App
- [配置](https://octo-agent.dev/docs/zh/reference/config-file/) · [权限](https://octo-agent.dev/docs/zh/reference/permissions/) · [工具](https://octo-agent.dev/docs/zh/reference/tools/)
- [架构](https://octo-agent.dev/docs/zh/architecture/system-layers/) —— 分层设计、provider 协议、如何扩展

## 开发

```bash
make build         # ./octo
make test          # go test -race ./...
```

项目约定见 [`CLAUDE.md`](CLAUDE.md) 与 [`.octorules`](.octorules)；PR 流程见
[`CONTRIBUTING.md`](CONTRIBUTING.md)。

## 致谢与前人工作

octo 站在两个项目的肩膀上，这点不遮掩：**[Claude Code](https://code.claude.com)**
—— agent 循环、工具集、SKILL.md 格式和整体 harness 行为塑造了 octo 的内部设计；
**[OpenClacky](https://github.com/clacky-ai/openclacky)** —— octo 的 UI 与交互
设计有很大一部分受它启发。有 bug 或者设计得不好的地方，都算 octo 自己的。

## 贡献者

感谢每一位为 octo 做出贡献的人：

<a href="https://github.com/open-octo/octo-agent/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=open-octo/octo-agent" alt="Contributors" />
</a>

## 许可

MIT。见 [`LICENSE.txt`](LICENSE.txt)。
